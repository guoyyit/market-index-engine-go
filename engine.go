package index

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotFound is returned when an index code is unknown.
var ErrNotFound = errors.New("index: not found")

// ErrExists is returned when an index code is already registered.
var ErrExists = errors.New("index: code already exists")

// ErrInvalidInput is returned for malformed caller input.
var ErrInvalidInput = errors.New("index: invalid input")

// snapshotEvery records a snapshot after this many ticks.
const snapshotEvery = 256

// Engine is the local index calculation and constituent management service.
// All public methods are safe for concurrent use; mutating operations are
// serialized and readers observe one consistent state at a time.
type Engine struct {
	mu                 sync.RWMutex
	store              *fileStore
	state              *engineState
	ticksSinceSnapshot int
}

// NewEngine opens (or creates) an engine backed by a local directory.
// Already-effective adjustments survive restarts because the state is
// reconstructed from the latest snapshot plus the remaining event log.
func NewEngine(dir string) (*Engine, error) {
	store, err := openStore(dir)
	if err != nil {
		return nil, err
	}

	seq, snap, err := store.loadSnapshot()
	if err != nil {
		return nil, err
	}
	state := newEngineState()
	if snap != nil {
		state = snap
		seq = state.Seq
	}

	events, err := store.eventsAfter(seq)
	if err != nil {
		return nil, err
	}
	var tickCount int
	if snap != nil {
		for _, st := range snap.Indices {
			tickCount += len(st.Ticks)
		}
	}
	for _, ev := range events {
		if err := applyEvent(state, ev); err != nil {
			return nil, fmt.Errorf("index: corrupt event seq=%d: %w", ev.Seq, err)
		}
		state.Seq = ev.Seq
		if ev.Type == EvTick {
			tickCount++
		}
	}
	tickCount %= snapshotEvery

	return &Engine{
		store:              store,
		state:              state,
		ticksSinceSnapshot: tickCount % snapshotEvery,
	}, nil
}

// Close flushes and releases the persistence resources.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.close()
}

// RegisterIndex creates an index with base constituents; the divisor is fixed
// so the base level equals def.BaseValue.
func (e *Engine) RegisterIndex(def IndexDef, base []Constituent, basePrices map[string]float64) error {
	if def.Code == "" || def.BaseValue <= 0 {
		return fmt.Errorf("%w: code required and base value must be positive", ErrInvalidInput)
	}
	if len(base) == 0 {
		return fmt.Errorf("%w: base constituents required", ErrInvalidInput)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.state.Indices[def.Code]; ok {
		return ErrExists
	}

	ev := Event{
		Type:       EvIndexRegistered,
		Time:       time.Now(),
		IndexCode:  def.Code,
		Def:        &def,
		Base:       cloneConstituents(base),
		BasePrices: clonePrices(basePrices),
	}
	if err := e.appendAndApply(ev); err != nil {
		return err
	}
	return nil
}

// AnnounceRebalance registers a constituent change with announce/effective dates.
func (e *Engine) AnnounceRebalance(code string, r Rebalance) error {
	if r.ID == "" || len(r.Constituents) == 0 || r.EffectiveDate.IsZero() {
		return fmt.Errorf("%w: rebalance id, effective date and constituents required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getState(code)
	if err != nil {
		return err
	}
	for _, s := range st.Scheduled {
		if s.ID == r.ID {
			return fmt.Errorf("%w: duplicate rebalance id %q", ErrInvalidInput, r.ID)
		}
	}
	ev := Event{
		Type:      EvRebalanceAnnounced,
		Time:      time.Now(),
		IndexCode: code,
		Rebalance: &r,
	}
	return e.appendAndApply(ev)
}

// RecordCorporateAction registers an announced corporate action.
func (e *Engine) RecordCorporateAction(code string, a CorporateAction) error {
	if a.ID == "" || a.Symbol == "" || a.ExDate.IsZero() {
		return fmt.Errorf("%w: action id, symbol and ex date required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getState(code)
	if err != nil {
		return err
	}
	for _, x := range st.Actions {
		if x.ID == a.ID {
			return fmt.Errorf("%w: duplicate action id %q", ErrInvalidInput, a.ID)
		}
	}
	ev := Event{
		Type:      EvActionRecorded,
		Time:      time.Now(),
		IndexCode: code,
		Action:    &a,
	}
	return e.appendAndApply(ev)
}

// OnTick feeds one local market observation; due rebalances/actions take effect
// atomically. Queries never observe a half-applied transition because the
// whole tick runs under the write lock.
func (e *Engine) OnTick(code string, at time.Time, prices map[string]float64) error {
	if at.IsZero() || prices == nil {
		return fmt.Errorf("%w: tick time and prices required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getState(code)
	if err != nil {
		return err
	}
	if !st.LastTime.IsZero() && at.Before(st.LastTime) {
		return fmt.Errorf("%w: ticks must be non-decreasing (last=%s, got=%s)",
			ErrInvalidInput, st.LastTime.Format(time.RFC3339), at.Format(time.RFC3339))
	}

	t := &Tick{Time: at, Prices: clonePrices(prices)}
	adjustments := applyTick(st, t)
	ev := Event{
		Type:        EvTick,
		Time:        time.Now(),
		IndexCode:   code,
		Tick:        t,
		Adjustments: adjustments,
	}
	if err := e.appendAndApply(ev); err != nil {
		return err
	}

	e.ticksSinceSnapshot++
	if e.ticksSinceSnapshot >= snapshotEvery {
		if err := e.store.saveSnapshot(e.state.Seq, e.state); err != nil {
			return err
		}
		e.ticksSinceSnapshot = 0
	}
	return nil
}

// PriceIndex returns the latest price index level.
func (e *Engine) PriceIndex(code string) (float64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, err := e.getState(code)
	if err != nil {
		return 0, err
	}
	return currentPrice(st), nil
}

// TotalReturnIndex returns the latest total return index level.
func (e *Engine) TotalReturnIndex(code string) (float64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, err := e.getState(code)
	if err != nil {
		return 0, err
	}
	return currentTotalReturn(st), nil
}

// CurrentConstituents returns the currently effective constituents and weights.
func (e *Engine) CurrentConstituents(code string) ([]Constituent, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, err := e.getState(code)
	if err != nil {
		return nil, err
	}
	return cloneConstituents(st.Current), nil
}

func (e *Engine) getState(code string) (*indexState, error) {
	st, ok := e.state.Indices[code]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, code)
	}
	return st, nil
}

func (e *Engine) appendAndApply(ev Event) error {
	ev.Seq = e.state.Seq + 1
	if err := e.store.append(ev); err != nil {
		return err
	}
	if err := applyEvent(e.state, ev); err != nil {
		return fmt.Errorf("index: failed to apply appended event: %w", err)
	}
	return nil
}

func newEngineState() *engineState {
	return &engineState{Indices: map[string]*indexState{}}
}

// applyTick is the single deterministic state-transition used by both live
// ingestion and event replay (including restart recovery).
func applyTick(st *indexState, t *Tick) []Adjustment {
	var prevPrices map[string]float64
	if n := len(st.Ticks); n > 0 {
		prevPrices = st.Ticks[n-1].Prices
	}

	// Corporate actions are applied first (they mutate the existing basket),
	// then rebalances switch baskets; both use the same observed prices and
	// occur atomically within this tick.
	adjustments := processDueActions(st, t.Time, prevPrices, t.Prices)
	adjustments = append(adjustments, processDueRebalances(st, t.Time, t.Prices)...)

	st.Ticks = append(st.Ticks, *t)
	st.LastTime = t.Time
	return adjustments
}

// applyEvent replays one persisted event onto the state.
func applyEvent(state *engineState, ev Event) error {
	state.Seq = ev.Seq
	switch ev.Type {
	case EvIndexRegistered:
		if ev.Def == nil {
			return errors.New("missing index definition")
		}
		if _, ok := state.Indices[ev.IndexCode]; ok {
			return fmt.Errorf("duplicate index %q", ev.IndexCode)
		}
		basket := applyWeightCap(ev.Base, ev.Def.WeightCap)
		sum := weightedSum(basket, ev.BasePrices)
		divisor := sum / ev.Def.BaseValue
		state.Indices[ev.IndexCode] = &indexState{
			Def:      *ev.Def,
			Current:  basket,
			Divisor:  divisor,
			TRFactor: 1,
		}
	case EvRebalanceAnnounced:
		st, ok := state.Indices[ev.IndexCode]
		if !ok {
			return fmt.Errorf("unknown index %q", ev.IndexCode)
		}
		st.Scheduled = append(st.Scheduled, *ev.Rebalance)
	case EvActionRecorded:
		st, ok := state.Indices[ev.IndexCode]
		if !ok {
			return fmt.Errorf("unknown index %q", ev.IndexCode)
		}
		st.Actions = append(st.Actions, *ev.Action)
	case EvTick:
		st, ok := state.Indices[ev.IndexCode]
		if !ok {
			return fmt.Errorf("unknown index %q", ev.IndexCode)
		}
		applyTick(st, ev.Tick)
	default:
		return fmt.Errorf("unknown event type %q", ev.Type)
	}
	return nil
}

func currentPrice(st *indexState) float64 {
	if len(st.Ticks) == 0 {
		return st.Def.BaseValue
	}
	return priceLevel(st.Current, st.Ticks[len(st.Ticks)-1].Prices, st.Divisor)
}

func currentTotalReturn(st *indexState) float64 {
	return currentPrice(st) * st.TRFactor
}

func cloneConstituents(in []Constituent) []Constituent {
	out := make([]Constituent, len(in))
	copy(out, in)
	return out
}

func clonePrices(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
