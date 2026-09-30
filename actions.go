package index

import (
	"sort"
	"time"
)

// processDueActions applies every recorded corporate action whose ex-date has
// been reached by the current tick, adjusting the divisor (splits, rights) or
// reinvesting cash into the total-return factor.
//
// Reference (cum) prices come from the previous tick; observed ex-date prices
// arrive in prices. Structural divisor changes are anchored to the theoretical
// ex price so only genuine market movement enters the index afterwards.
//
// Rules:
//   - cash dividend D: weights/divisor unchanged for the price index; the
//     total return factor is multiplied by (Pex+D)/Pex.
//   - split/bonus issue with ratio k: notional holding is multiplied by k;
//     theoretical ex price is Pcum/k; divisor adjusted for continuity.
//   - rights issue r shares per old share at Ps: holding multiplied by 1+r;
//     theoretical ex price is (Pcum+r*Ps)/(1+r); divisor adjusted likewise.
func processDueActions(st *indexState, at time.Time, prevPrices, prices map[string]float64) []Adjustment {
	due := make([]int, 0)
	for i := range st.Actions {
		if !st.Actions[i].Applied && !st.Actions[i].ExDate.After(at) {
			due = append(due, i)
		}
	}
	if len(due) == 0 {
		return nil
	}
	sort.Slice(due, func(a, b int) bool {
		x, y := st.Actions[due[a]], st.Actions[due[b]]
		if !x.ExDate.Equal(y.ExDate) {
			return x.ExDate.Before(y.ExDate)
		}
		if x.ID != y.ID {
			return x.ID < y.ID
		}
		return x.Symbol < y.Symbol
	})

	adjustments := make([]Adjustment, 0, len(due))
	for _, idx := range due {
		a := st.Actions[idx]
		w := weightOf(st.Current, a.Symbol)
		pcum, haveCum := prevPrices[a.Symbol]
		pex := prices[a.Symbol]

		oldDivisor := st.Divisor
		oldTR := st.TRFactor
		oldSum := weightedSum(st.Current, prevPrices)
		newSum := oldSum

		switch a.Type {
		case ActionCashDividend:
			// Reinvest the dividend at the observed ex price.
			if pex > 0 && a.CashPerShare > 0 {
				st.TRFactor *= (pex + a.CashPerShare) / pex
			}
		case ActionSplit:
			if a.SplitRatio > 0 && w > 0 {
				scaleHolding(st, a.Symbol, a.SplitRatio)
				if haveCum && pcum > 0 {
					pRef := pcum / a.SplitRatio
					newSum = oldSum - w*pcum + w*a.SplitRatio*pRef
				}
			}
		case ActionRightsIssue:
			if a.RightsRatio > 0 && w > 0 {
				factor := 1 + a.RightsRatio
				scaleHolding(st, a.Symbol, factor)
				if haveCum && pcum > 0 {
					pRef := (pcum + a.RightsRatio*a.RightsPrice) / factor
					newSum = oldSum - w*pcum + w*factor*pRef
				}
			}
		}

		newDivisor := oldDivisor
		if oldSum != 0 && newSum != oldSum {
			newDivisor = adjustDivisor(oldDivisor, oldSum, newSum)
		}
		st.Divisor = newDivisor
		st.Actions[idx].Applied = true

		adjustments = append(adjustments, Adjustment{
			Reason:      string(a.Type) + ":" + a.ID,
			Symbol:      a.Symbol,
			OldSum:      oldSum,
			NewSum:      newSum,
			OldDivisor:  oldDivisor,
			NewDivisor:  newDivisor,
			OldTRFactor: oldTR,
			NewTRFactor: st.TRFactor,
		})
	}
	return adjustments
}

func weightOf(cons []Constituent, symbol string) float64 {
	for _, c := range cons {
		if c.Symbol == symbol {
			return c.Weight
		}
	}
	return 0
}

func scaleHolding(st *indexState, symbol string, factor float64) {
	for i := range st.Current {
		if st.Current[i].Symbol == symbol {
			st.Current[i].Weight *= factor
			return
		}
	}
}
