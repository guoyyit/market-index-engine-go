package index

import (
	"fmt"
	"sort"
	"time"
)

// loadEvents 读取目录下全部持久化事件，按 (时间, 序号) 确定唯一顺序。
func loadEvents(dir string) ([]Event, error) {
	store, events, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	sortEvents(events)
	return events, nil
}

// sortEvents 按 (时间, 序号) 升序排列，保证重建顺序唯一确定。
func sortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, events[i].At)
		tj, _ := time.Parse(time.RFC3339Nano, events[j].At)
		if ti.Equal(tj) {
			return events[i].Seq < events[j].Seq
		}
		return ti.Before(tj)
	})
}

// RebuildAt 从持久化日志重建截至 at（含）时点的指数值。
// 重建与实时计算共用同一条确定性应用路径（applyEvent），
// 因此同一输入下逐点结果必须一致；快照仅用于跳过前缀事件，不改变结果。
func (engine *Engine) RebuildAt(at time.Time) (IndexValue, error) {
	if engine.store == nil {
		return IndexValue{}, fmt.Errorf("rebuild requires a persistent store")
	}
	events, err := loadEvents(engine.store.dir)
	if err != nil {
		return IndexValue{}, err
	}

	rebuilt := &Engine{state: newState(0)}
	applied := 0

	// 快照优化：若快照完全位于 at 之前，从快照状态继续；否则从空状态全量重放。
	snapshot, _ := LoadSnapshot(engine.store.dir)
	if snapshot != nil && snapshot.Initialized {
		snapshotTime, errSnapshotTime := time.Parse(time.RFC3339Nano, snapshot.LastQuoteAt)
		if errSnapshotTime == nil && !snapshotTime.After(at) {
			rebuilt.applySnapshot(snapshot)
			applied = int(snapshot.Seq)
		}
	}

	// 截止时点 = 不晚于 at 的最后一个行情（或基期）时点。
	// 公告/登记类事件只有在不晚于该时点时才“已知”，故先确定 cutoff，
	// 再统一应用 cutoff 之前（含）的所有事件，避免未来信息提前可见。
	cutoff := time.Time{}
	for _, event := range events {
		eventAt, err := time.Parse(time.RFC3339Nano, event.At)
		if err != nil {
			return IndexValue{}, err
		}
		if (event.Type == eventQuote || event.Type == eventBase) &&
			!eventAt.After(at) && eventAt.After(cutoff) {
			cutoff = eventAt
		}
	}

	for _, event := range events {
		eventAt, err := time.Parse(time.RFC3339Nano, event.At)
		if err != nil {
			return IndexValue{}, err
		}
		if eventAt.After(cutoff) || event.Seq <= int64(applied) {
			continue
		}
		if err := rebuilt.applyEvent(event); err != nil {
			return IndexValue{}, fmt.Errorf("rebuild seq %d: %w", event.Seq, err)
		}
	}

	st := rebuilt.state
	if !st.initialized {
		return IndexValue{}, ErrNotInitialized
	}
	return IndexValue{
		At:           cutoff,
		Price:        st.price.level(st.prices),
		TotalReturn:  st.tr.level(st.prices),
		PriceDivisor: st.price.divisor,
		TRDivisor:    st.tr.divisor,
	}, nil
}

// RebuildRange 重建 [from, to] 区间内每个行情时点的指数值，
// 一次顺序重放完成，避免逐点重复扫描日志。
func (engine *Engine) RebuildRange(from, to time.Time) ([]IndexValue, error) {
	if engine.store == nil {
		return nil, fmt.Errorf("rebuild requires a persistent store")
	}
	if to.Before(from) {
		return nil, fmt.Errorf("rebuild range: to before from")
	}
	events, err := loadEvents(engine.store.dir)
	if err != nil {
		return nil, err
	}
	rebuilt := &Engine{state: newState(0)}
	var values []IndexValue
	var quoteAt time.Time
	hasQuote := false
	flush := func(at time.Time) {
		st := rebuilt.state
		values = append(values, IndexValue{
			At:           at,
			Price:        st.price.level(st.prices),
			TotalReturn:  st.tr.level(st.prices),
			PriceDivisor: st.price.divisor,
			TRDivisor:    st.tr.divisor,
		})
	}
	for _, event := range events {
		eventAt, err := time.Parse(time.RFC3339Nano, event.At)
		if err != nil {
			return nil, err
		}
		// 时间戳切换时，先输出上一时点处理完所有事件后的一致状态。
		if hasQuote && !eventAt.Equal(quoteAt) {
			flush(quoteAt)
			hasQuote = false
		}
		if eventAt.Before(from) {
			if err := rebuilt.applyEvent(event); err != nil {
				return nil, fmt.Errorf("rebuild seq %d: %w", event.Seq, err)
			}
			continue
		}
		if eventAt.After(to) {
			break
		}
		if err := rebuilt.applyEvent(event); err != nil {
			return nil, fmt.Errorf("rebuild seq %d: %w", event.Seq, err)
		}
		if event.Type == eventBase && !eventAt.Before(from) && !eventAt.After(to) {
			st := rebuilt.state
			values = append(values, IndexValue{
				At:           st.lastQuoteAt,
				Price:        st.price.level(st.prices),
				TotalReturn:  st.tr.level(st.prices),
				PriceDivisor: st.price.divisor,
				TRDivisor:    st.tr.divisor,
			})
			continue
		}
		if event.Type == eventQuote {
			quoteAt = eventAt
			hasQuote = true
		}
	}
	if hasQuote && !quoteAt.Before(from) && !quoteAt.After(to) {
		flush(quoteAt)
	}
	return values, nil
}
