package index

import (
	"sort"
	"time"
)

// processDueRebalances atomically switches constituents at their effective
// date, adjusting the divisor so the index level does not jump.
func processDueRebalances(st *indexState, at time.Time, prices map[string]float64) []Adjustment {
	var due, pending []Rebalance
	for _, r := range st.Scheduled {
		if !r.EffectiveDate.After(at) {
			due = append(due, r)
		} else {
			pending = append(pending, r)
		}
	}
	if len(due) == 0 {
		return nil
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].EffectiveDate.Equal(due[j].EffectiveDate) {
			return due[i].EffectiveDate.Before(due[j].EffectiveDate)
		}
		return due[i].ID < due[j].ID
	})

	adjustments := make([]Adjustment, 0, len(due))
	for _, r := range due {
		// Target weights are normalized and capped through the same
		// adjustment-coefficient path used for the base basket.
		next := applyWeightCap(r.Constituents, st.Def.WeightCap)

		oldSum := weightedSum(st.Current, prices)
		newSum := weightedSum(next, prices)
		oldDivisor := st.Divisor
		newDivisor := oldDivisor
		if oldSum != 0 && newSum != 0 {
			newDivisor = adjustDivisor(oldDivisor, oldSum, newSum)
		}

		st.Current = next
		st.Divisor = newDivisor
		adjustments = append(adjustments, Adjustment{
			Reason:      "rebalance:" + r.ID,
			OldSum:      oldSum,
			NewSum:      newSum,
			OldDivisor:  oldDivisor,
			NewDivisor:  newDivisor,
			OldTRFactor: st.TRFactor,
			NewTRFactor: st.TRFactor,
		})
	}

	// The whole batch becomes visible at once: scheduled list and current
	// basket are replaced together.
	st.Scheduled = pending
	return adjustments
}
