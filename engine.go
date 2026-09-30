package index

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrAlreadyInitialized 指数基期只能初始化一次。
	ErrAlreadyInitialized = errors.New("index already initialized")
	// ErrNotInitialized 指数尚未初始化，无法计算或调样。
	ErrNotInitialized = errors.New("index not initialized")
	// ErrOutOfOrderQuote 行情时间早于上一条行情，实时流必须按时间单调推进。
	ErrOutOfOrderQuote = errors.New("quote timestamp out of order")
	// ErrUnknownInstrument 行情或调样引用了未登记的证券。
	ErrUnknownInstrument = errors.New("unknown instrument")
)

// state 是指数的全部可变计算状态。持锁访问；每次原子变更整体完成后才释放锁。
type state struct {
	capFraction float64
	instruments map[string]Instrument
	price       *series
	tr          *series
	initialized bool

	pendingChanges  map[string]ConstituentChange
	pendingActions  map[string]CorporateAction
	appliedChangeID map[string]bool
	appliedActionID map[string]bool
	prices          map[string]float64 // 每只成分的最新价
	lastQuoteAt     time.Time
}

func newState(capFraction float64) *state {
	return &state{
		capFraction:     capFraction,
		instruments:     map[string]Instrument{},
		price:           newSeries(0),
		tr:              newSeries(0),
		pendingChanges:  map[string]ConstituentChange{},
		pendingActions:  map[string]CorporateAction{},
		appliedChangeID: map[string]bool{},
		appliedActionID: map[string]bool{},
		prices:          map[string]float64{},
	}
}

// Engine 是指数计算与成分管理服务入口。
// 所有方法并发安全；任何查询读到的都是某次完整变更后的一致状态。
type Engine struct {
	mu    sync.RWMutex
	state *state
	store *Store
}

// NewEngine 创建空引擎（不落盘），主要用于测试或纯内存使用。
func NewEngine(capFraction float64) *Engine {
	return &Engine{state: newState(capFraction)}
}

// OpenEngine 从本地目录恢复：先尝试快照，再重放其后事件。
// 重启后已生效的调样与公司行为不会丢失，也不会重复应用。
func OpenEngine(dir string) (*Engine, error) {
	store, events, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	engine := &Engine{state: newState(0), store: store}

	snapshot, err := LoadSnapshot(dir)
	if err != nil {
		return nil, err
	}
	startSeq := int64(0)
	if snapshot != nil {
		engine.applySnapshot(snapshot)
		startSeq = snapshot.Seq
	}
	for _, event := range events {
		if event.Seq <= startSeq {
			continue
		}
		if err := engine.applyEvent(event); err != nil {
			return nil, fmt.Errorf("replay seq %d: %w", event.Seq, err)
		}
	}
	return engine, nil
}

// Close 关闭底层事件日志。
func (engine *Engine) Close() error {
	if engine.store != nil {
		return engine.store.Close()
	}
	return nil
}

// RegisterInstrument 登记一只本地证券。
func (engine *Engine) RegisterInstrument(instrument Instrument) error {
	if instrument.Symbol == "" {
		return errors.New("instrument symbol required")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.state.instruments[instrument.Symbol] = instrument
	if engine.store != nil {
		if _, err := engine.store.Append(Event{
			Type:       eventInstrument,
			At:         time.Now().UTC().Format(time.RFC3339Nano),
			Instrument: &instrument,
		}); err != nil {
			delete(engine.state.instruments, instrument.Symbol)
			return err
		}
	}
	return nil
}

// Initialize 在基期按目标权重与基期价格建立双口径指数，基期点位 DefaultBaseLevel。
func (engine *Engine) Initialize(weights []Weight, baseQuotes []Quote) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	st := engine.state
	if st.initialized {
		return ErrAlreadyInitialized
	}
	target := make(map[string]float64, len(weights))
	for _, entry := range weights {
		if _, ok := st.instruments[entry.Symbol]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstrument, entry.Symbol)
		}
		target[entry.Symbol] = entry.Weight
	}
	capped, _, err := applyCap(target, st.capFraction)
	if err != nil {
		return err
	}
	prices := map[string]float64{}
	var baseAt time.Time
	for _, quote := range baseQuotes {
		prices[quote.Symbol] = quote.Price
		if quote.At.After(baseAt) {
			baseAt = quote.At
		}
	}
	if err := st.price.initAtBase(capped, prices, DefaultBaseLevel); err != nil {
		return err
	}
	if err := st.tr.initAtBase(capped, prices, DefaultBaseLevel); err != nil {
		return err
	}
	for symbol, price := range prices {
		st.prices[symbol] = price
	}
	st.lastQuoteAt = baseAt
	st.initialized = true

	if engine.store != nil {
		event := Event{
			Type:    eventBase,
			At:      baseAt.UTC().Format(time.RFC3339Nano),
			Weights: weightList(capped),
			Quotes:  quoteList(prices, baseAt),
			Cap:     st.capFraction,
		}
		if _, err := engine.store.Append(event); err != nil {
			st.initialized = false
			return err
		}
	}
	return nil
}

// SubmitChange 登记一张带公告日/生效日的成分调样单。
// 生效日之前不改变任何计算状态；生效日随下一条行情原子切换。
func (engine *Engine) SubmitChange(change ConstituentChange) error {
	if change.ID == "" {
		return errors.New("change id required")
	}
	if !change.EffectiveAt.After(change.AnnouncedAt) {
		return errors.New("effective time must be after announcement time")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	st := engine.state
	if st.appliedChangeID[change.ID] || st.pendingChanges[change.ID].ID != "" {
		return fmt.Errorf("%w: change %s", ErrEventExists, change.ID)
	}
	for _, add := range change.Adds {
		if _, ok := st.instruments[add.Symbol]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstrument, add.Symbol)
		}
	}
	st.pendingChanges[change.ID] = change
	if engine.store != nil {
		if _, err := engine.store.Append(Event{
			Type:   eventChangeSubmit,
			At:     change.AnnouncedAt.UTC().Format(time.RFC3339Nano),
			Change: &change,
		}); err != nil {
			delete(st.pendingChanges, change.ID)
			return err
		}
	}
	return nil
}

// RecordAction 登记一次公司行为（公告日录入，除权除息日应用）。
func (engine *Engine) RecordAction(action CorporateAction) error {
	if action.ID == "" || action.Symbol == "" {
		return errors.New("action id and symbol required")
	}
	if !action.ExDate.After(action.AnnouncedAt) {
		return errors.New("ex date must be after announcement date")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	st := engine.state
	if st.appliedActionID[action.ID] || st.pendingActions[action.ID].ID != "" {
		return fmt.Errorf("%w: action %s", ErrEventExists, action.ID)
	}
	if _, ok := st.instruments[action.Symbol]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownInstrument, action.Symbol)
	}
	st.pendingActions[action.ID] = action
	if engine.store != nil {
		if _, err := engine.store.Append(Event{
			Type:     eventActionSubmit,
			At:       action.AnnouncedAt.UTC().Format(time.RFC3339Nano),
			ChangeID: action.ID,
			Action:   &action,
		}); err != nil {
			delete(st.pendingActions, action.ID)
			return err
		}
	}
	return nil
}

// ApplyQuote 推送一条本地行情。推送时先原子应用所有到期的公司行为，
// 再原子切换所有到生效日的调样单，最后更新价格。
func (engine *Engine) ApplyQuote(quote Quote) error {
	if quote.Symbol == "" {
		return errors.New("quote symbol required")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	st := engine.state
	if !st.initialized {
		return ErrNotInitialized
	}
	if _, ok := st.instruments[quote.Symbol]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownInstrument, quote.Symbol)
	}
	if quote.At.Before(st.lastQuoteAt) {
		return ErrOutOfOrderQuote
	}

	// 顺序固定且在同一把锁内完成：
	// 1) 应用到期公司行为——必须在价格表更新前抓取除息前收盘价；
	// 2) 写入本次价格——使生效日纳入的新成分可立即定价；
	// 3) 原子切换到期调样。任何查询都读不到这之间的中间状态。
	dueActions, err := st.applyDueActions(quote.At)
	if err != nil {
		return err
	}

	st.prices[quote.Symbol] = quote.Price
	st.lastQuoteAt = quote.At

	dueChanges, err := st.applyDueChanges(quote.At)
	if err != nil {
		return err
	}

	if engine.store != nil {
		// 持久化顺序必须与重放计算顺序一致：先记录行情，再记录该行情
		// 触发的公司行为与调样。时间戳统一取行情时间，使 sortEvents
		// 按 (时间, seq) 还原出确定的计算过程。
		if _, err := engine.store.Append(Event{
			Type:  eventQuote,
			At:    quote.At.UTC().Format(time.RFC3339Nano),
			Quote: &quote,
		}); err != nil {
			return err
		}
		for _, action := range dueActions {
			if _, err := engine.store.Append(Event{
				Type:   eventAction,
				At:     quote.At.UTC().Format(time.RFC3339Nano),
				Action: &action,
			}); err != nil {
				return err
			}
		}
		for _, change := range dueChanges {
			if _, err := engine.store.Append(Event{
				Type:     eventReweight,
				At:       quote.At.UTC().Format(time.RFC3339Nano),
				Weights:  st.currentWeightList(),
				ChangeID: change.ID,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Level 返回当前最新时点的双口径指数值。
func (engine *Engine) Level() IndexValue {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	st := engine.state
	return IndexValue{
		At:           st.lastQuoteAt,
		Price:        st.price.level(st.prices),
		TotalReturn:  st.tr.level(st.prices),
		PriceDivisor: st.price.divisor,
		TRDivisor:    st.tr.divisor,
	}
}

// ReinvestmentFactor 返回全收益/价格的累计换算系数，
// 恒等于 TotalReturn / Price，且只由分红再投资累积而来。
func (engine *Engine) ReinvestmentFactor() float64 {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	st := engine.state
	priceLevel := st.price.level(st.prices)
	if priceLevel == 0 {
		return 1
	}
	return st.tr.level(st.prices) / priceLevel
}

// Constituents 返回当前已生效成分及其封顶后权重的快照副本。
func (engine *Engine) Constituents() map[string]float64 {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	out := make(map[string]float64, len(engine.state.price.shares))
	for symbol := range engine.state.price.shares {
		out[symbol] = engine.state.weightOf(symbol)
	}
	return out
}

// saveSnapshotLocked 在调用方持锁时保存状态快照。
func (engine *Engine) saveSnapshotLocked() error {
	if engine.store == nil {
		return nil
	}
	return engine.store.SaveSnapshot(engine.state.snapshot(engine.store.Seq()))
}

// Checkpoint 显式保存一次原子快照，用于缩短重启重放区间。
// 快照只影响性能，不影响任何重建结果（重放事件可得到相同状态）。
func (engine *Engine) Checkpoint() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.saveSnapshotLocked()
}

func (st *state) weightOf(symbol string) float64 {
	held, ok := st.price.shares[symbol]
	if !ok {
		return 0
	}
	price := st.prices[symbol]
	total := st.price.marketValue(st.prices)
	if total == 0 {
		return 0
	}
	return held * price / total
}

func (st *state) currentWeightList() []Weight {
	symbols := make([]string, 0, len(st.price.shares))
	for symbol := range st.price.shares {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	out := make([]Weight, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, Weight{Symbol: symbol, Weight: st.weightOf(symbol)})
	}
	return out
}

func weightList(weights map[string]float64) []Weight {
	symbols := make([]string, 0, len(weights))
	for symbol := range weights {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	out := make([]Weight, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, Weight{Symbol: symbol, Weight: weights[symbol]})
	}
	return out
}

func quoteList(prices map[string]float64, at time.Time) []Quote {
	symbols := make([]string, 0, len(prices))
	for symbol := range prices {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	out := make([]Quote, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, Quote{Symbol: symbol, At: at, Price: prices[symbol]})
	}
	return out
}

// applyDueActions 应用所有除权除息日 <= now 的待处理公司行为，按除息日、ID 排序。
// 每个行为用登记时保存的除息前价格计算股数系数，两条口径同步调整后原子可见。
func (st *state) applyDueActions(now time.Time) ([]CorporateAction, error) {
	due := make([]CorporateAction, 0)
	for _, action := range st.pendingActions {
		if !action.ExDate.After(now) {
			due = append(due, action)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].ExDate.Equal(due[j].ExDate) {
			return due[i].ID < due[j].ID
		}
		return due[i].ExDate.Before(due[j].ExDate)
	})
	for index := range due {
		action := due[index]
		cumPrice := action.CumPrice
		if cumPrice <= 0 {
			cumPrice = st.prices[action.Symbol] // 除息前最新收盘价
			action.CumPrice = cumPrice
		}
		factors, err := actionShareFactors(action, cumPrice)
		if err != nil {
			return nil, fmt.Errorf("action %s on %s: %w", action.ID, action.Symbol, err)
		}
		st.price.scaleShares(action.Symbol, factors.price)
		st.tr.scaleShares(action.Symbol, factors.tr)
		due[index] = action
		st.appliedActionID[action.ID] = true
		delete(st.pendingActions, action.ID)
	}
	return due, nil
}

// applyDueChanges 原子应用所有生效日 <= now 的调样单：合并目标权重、
// 施加封顶、按同一组价格重设股数并修正两条口径的除数，切换瞬间点位不跳变。
func (st *state) applyDueChanges(now time.Time) ([]ConstituentChange, error) {
	due := make([]ConstituentChange, 0)
	for _, change := range st.pendingChanges {
		if !change.EffectiveAt.After(now) {
			due = append(due, change) // 值拷贝入切片
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].EffectiveAt.Equal(due[j].EffectiveAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].EffectiveAt.Before(due[j].EffectiveAt)
	})
	appliedCount := 0
	for index := 0; index < len(due); index++ {
		change := due[index]
		target := map[string]float64{}
		for symbol := range st.price.shares {
			target[symbol] = st.weightOf(symbol)
		}
		for _, removed := range change.Removes {
			delete(target, removed)
		}
		for _, add := range change.Adds {
			target[add.Symbol] = add.Weight
		}
		for symbol, weight := range change.WeightPatch {
			if _, keep := target[symbol]; keep {
				target[symbol] = weight
			}
		}
		// 生效日行情是逐只到达的：新成分价格到齐前不得切换，
		// 否则会出现除数无法计算的半成品状态。按生效日顺序处理，
		// 前一张单未就绪时，后续单据也一并等待，下一条行情再重试。
		ready := true
		for symbol := range target {
			if st.prices[symbol] <= 0 {
				ready = false
				break
			}
		}
		if !ready {
			return due[:appliedCount], nil
		}
		capped, _, err := applyCap(target, st.capFraction)
		if err != nil {
			return nil, fmt.Errorf("change %s: %w", change.ID, err)
		}
		if err := st.price.reweight(capped, st.prices); err != nil {
			return nil, fmt.Errorf("change %s: %w", change.ID, err)
		}
		if err := st.tr.reweight(capped, st.prices); err != nil {
			return nil, fmt.Errorf("change %s: %w", change.ID, err)
		}
		for _, removed := range change.Removes {
			delete(st.prices, removed)
		}
		st.appliedChangeID[change.ID] = true
		delete(st.pendingChanges, change.ID)
		appliedCount++
	}
	return due[:appliedCount], nil
}

// applyEvent 重放单条已持久化事件（重启恢复与历史重建共用同一条确定性路径）。
func (engine *Engine) applyEvent(event Event) error {
	st := engine.state
	at, err := time.Parse(time.RFC3339Nano, event.At)
	if err != nil {
		return fmt.Errorf("parse event time: %w", err)
	}
	switch event.Type {
	case eventInstrument:
		if event.Instrument != nil {
			st.instruments[event.Instrument.Symbol] = *event.Instrument
		}
	case eventChangeSubmit:
		if event.Change != nil {
			st.pendingChanges[event.Change.ID] = *event.Change
		}
	case eventActionSubmit:
		if event.Action != nil {
			st.pendingActions[event.Action.ID] = *event.Action
		}
	case eventBase:
		weights := map[string]float64{}
		for _, entry := range event.Weights {
			weights[entry.Symbol] = entry.Weight
		}
		prices := map[string]float64{}
		for _, quote := range event.Quotes {
			prices[quote.Symbol] = quote.Price
		}
		st.capFraction = event.Cap
		if err := st.price.initAtBase(weights, prices, DefaultBaseLevel); err != nil {
			return err
		}
		if err := st.tr.initAtBase(weights, prices, DefaultBaseLevel); err != nil {
			return err
		}
		for symbol, price := range prices {
			st.prices[symbol] = price
		}
		st.lastQuoteAt = at
		st.initialized = true
	case eventQuote:
		if event.Quote == nil {
			return errors.New("quote event missing payload")
		}
		// 该行情触发的公司行为/调样由紧随其后的 action/reweight 事件表达，
		// 此处仅更新价格，避免重放时重复应用。
		st.prices[event.Quote.Symbol] = event.Quote.Price
		st.lastQuoteAt = at
	case eventAction:
		if event.Action == nil {
			return errors.New("action event missing payload")
		}
		action := *event.Action
		factors, err := actionShareFactors(action, action.CumPrice)
		if err != nil {
			return err
		}
		st.price.scaleShares(action.Symbol, factors.price)
		st.tr.scaleShares(action.Symbol, factors.tr)
		st.appliedActionID[action.ID] = true
		delete(st.pendingActions, action.ID)
	case eventReweight:
		weights := map[string]float64{}
		for _, entry := range event.Weights {
			weights[entry.Symbol] = entry.Weight
		}
		if err := st.price.reweight(weights, st.prices); err != nil {
			return err
		}
		if err := st.tr.reweight(weights, st.prices); err != nil {
			return err
		}
		if event.ChangeID != "" {
			st.appliedChangeID[event.ChangeID] = true
			delete(st.pendingChanges, event.ChangeID)
		}
	default:
		return fmt.Errorf("unknown event type %q", event.Type)
	}
	return nil
}

func (st *state) snapshot(seq int64) Snapshot {
	pendingChanges := make(map[string]ConstituentChange, len(st.pendingChanges))
	for id, change := range st.pendingChanges {
		pendingChanges[id] = change
	}
	pendingActions := make(map[string]CorporateAction, len(st.pendingActions))
	for id, action := range st.pendingActions {
		pendingActions[id] = action
	}
	appliedChanges := make(map[string]bool, len(st.appliedChangeID))
	for id := range st.appliedChangeID {
		appliedChanges[id] = true
	}
	appliedActions := make(map[string]bool, len(st.appliedActionID))
	for id := range st.appliedActionID {
		appliedActions[id] = true
	}
	prices := make(map[string]float64, len(st.prices))
	for symbol, price := range st.prices {
		prices[symbol] = price
	}
	return Snapshot{
		Seq:            seq,
		Cap:            st.capFraction,
		Instruments:    st.instruments,
		PriceShares:    st.price.shares,
		TRShares:       st.tr.shares,
		PriceDivisor:   st.price.divisor,
		TRDivisor:      st.tr.divisor,
		Initialized:    st.initialized,
		PendingChanges: pendingChanges,
		PendingActions: pendingActions,
		AppliedChanges: appliedChanges,
		AppliedActions: appliedActions,
		Prices:         prices,
		LastQuoteAt:    st.lastQuoteAt.UTC().Format(time.RFC3339Nano),
	}
}

func (engine *Engine) applySnapshot(snapshot *Snapshot) {
	st := newState(snapshot.Cap)
	st.instruments = snapshot.Instruments
	st.price.shares = snapshot.PriceShares
	st.tr.shares = snapshot.TRShares
	st.price.divisor = snapshot.PriceDivisor
	st.tr.divisor = snapshot.TRDivisor
	st.initialized = snapshot.Initialized
	if snapshot.PendingChanges != nil {
		st.pendingChanges = snapshot.PendingChanges
	}
	if snapshot.PendingActions != nil {
		st.pendingActions = snapshot.PendingActions
	}
	if snapshot.AppliedChanges != nil {
		st.appliedChangeID = snapshot.AppliedChanges
	}
	if snapshot.AppliedActions != nil {
		st.appliedActionID = snapshot.AppliedActions
	}
	if snapshot.Prices != nil {
		st.prices = snapshot.Prices
	}
	if snapshot.Instruments != nil {
		st.instruments = snapshot.Instruments
	}
	if snapshot.LastQuoteAt != "" {
		if at, err := time.Parse(time.RFC3339Nano, snapshot.LastQuoteAt); err == nil {
			st.lastQuoteAt = at
		}
	}
	engine.state = st
}
