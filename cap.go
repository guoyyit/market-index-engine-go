package index

// capConvergenceTolerance bounds the capped-weight error.
const capConvergenceTolerance = 1e-12

// capMaxIterations bounds the coefficient solver.
const capMaxIterations = 10000

// applyWeightCap returns normalized weights after capping any single
// constituent at capLimit (0 < capLimit < 1) via per-name adjustment
// coefficients.
//
// Each name receives an adjustment coefficient a_i; its final weight is
// w_i*a_i / Σ w_j*a_j. Capped names are pinned (final weight == capLimit)
// and the freed weight is redistributed to the uncapped names in proportion
// to their original weights. The fixed-point iteration is monotone and
// converges; the returned slice always sums to 1.
func applyWeightCap(cons []Constituent, capLimit float64) []Constituent {
	n := len(cons)
	out := make([]Constituent, n)
	if n == 0 {
		return out
	}

	var totalWeight float64
	for _, c := range cons {
		totalWeight += c.Weight
	}
	if totalWeight <= 0 {
		equal := 1.0 / float64(n)
		for i, c := range cons {
			out[i] = Constituent{Symbol: c.Symbol, Weight: equal}
		}
		return out
	}

	for i, c := range cons {
		out[i] = Constituent{Symbol: c.Symbol, Weight: c.Weight / totalWeight}
	}

	// With no effective cap the normalized weights are the final answer.
	if capLimit <= 0 || capLimit >= 1 {
		return out
	}

	// A single name must end with exactly the cap; avoid a degenerate loop.
	if n == 1 {
		out[0].Weight = 1
		return out
	}

	// A cap below equal weight is infeasible (the weights could never sum to
	// 1), so the effective floor is 1/n.
	if minCap := 1.0 / float64(n); capLimit < minCap {
		capLimit = minCap
	}

	// Iterative redistribution. capped[i] records names already pinned at the
	// limit; each round their weight is held fixed and the remaining budget is
	// spread over uncapped names in proportion to their original weights.
	capped := make([]bool, n)
	weights := make([]float64, n)
	copy(weights, normalizeWeights(out))

	for iter := 0; iter < capMaxIterations; iter++ {
		var cappedSum float64
		for i, w := range weights {
			if capped[i] {
				cappedSum += w
			}
		}

		var freeOrig float64
		for i, c := range out {
			if !capped[i] {
				freeOrig += c.Weight
			}
		}
		freeBudget := 1 - cappedSum

		newlyCapped := false
		for i := range out {
			if capped[i] {
				continue
			}
			w := out[i].Weight / freeOrig * freeBudget
			weights[i] = w
			if w > capLimit+capConvergenceTolerance {
				capped[i] = true
				weights[i] = capLimit
				newlyCapped = true
			}
		}
		if !newlyCapped {
			break
		}
	}

	// Renormalize once at the end so the weights sum to 1 exactly.
	var sum float64
	for _, w := range weights {
		sum += w
	}
	for i := range out {
		out[i].Weight = weights[i] / sum
	}
	return out
}

func normalizeWeights(cons []Constituent) []float64 {
	var sum float64
	for _, c := range cons {
		sum += c.Weight
	}
	w := make([]float64, len(cons))
	if sum <= 0 {
		for i := range w {
			w[i] = 1 / float64(len(cons))
		}
		return w
	}
	for i, c := range cons {
		w[i] = c.Weight / sum
	}
	return w
}
