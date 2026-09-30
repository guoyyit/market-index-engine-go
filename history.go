package index

import "time"

// Rebuild replays persisted inputs from scratch (full event log, ignoring any
// snapshot) and returns the index level at every observed tick. The same
// applyEvent transition powers live ingestion and reconstruction, so under
// identical inputs they agree point by point. Snapshots only reduce restart
// replay work, never the result.
func (e *Engine) Rebuild(code string) ([]LevelPoint, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.replayPoints(code, time.Time{}, time.Time{})
}

// RebuildRange reconstructs index points with start <= t <= end. A zero bound
// means open-ended.
func (e *Engine) RebuildRange(code string, start, end time.Time) ([]LevelPoint, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.replayPoints(code, start, end)

}

// RebuildAt reconstructs both measures at an arbitrary historical time. The
// returned point reflects the latest tick at or before at.
func (e *Engine) RebuildAt(code string, at time.Time) (LevelPoint, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	points, err := e.replayPoints(code, time.Time{}, at)
	if err != nil {
		return LevelPoint{}, err
	}
	if len(points) == 0 {
		return LevelPoint{Time: at}, nil
	}
	return points[len(points)-1], nil
}

func (e *Engine) replayPoints(code string, start, end time.Time) ([]LevelPoint, error) {
	if _, ok := e.state.Indices[code]; !ok {
		return nil, ErrNotFound
	}

	events, err := e.store.eventsAfter(0)
	if err != nil {
		return nil, err
	}

	replay := newEngineState()
	points := make([]LevelPoint, 0)
	for _, ev := range events {
		if err := applyEvent(replay, ev); err != nil {
			return nil, err
		}
		if ev.Type != EvTick || ev.IndexCode != code {
			continue
		}
		s := replay.Indices[code]
		tt := s.Ticks[len(s.Ticks)-1].Time
		if (!start.IsZero() && tt.Before(start)) || (!end.IsZero() && tt.After(end)) {
			continue
		}
		p := priceLevel(s.Current, s.Ticks[len(s.Ticks)-1].Prices, s.Divisor)
		points = append(points, LevelPoint{
			Time:        tt,
			Price:       p,
			TotalReturn: p * s.TRFactor,
		})
	}
	return points, nil
}
