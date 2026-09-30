package index

import (
	"errors"
	"math"
	"sort"
)

var (
	// ErrCapInfeasible 表示上限小于平均权重，任何归一权重都无法满足封顶。
	ErrCapInfeasible = errors.New("weight cap is below 1/N, infeasible")
	// ErrEmptyWeights 表示没有任何权重参与归一。
	ErrEmptyWeights = errors.New("empty weight set")
)

const (
	capConvergenceEps = 1e-12
	factorOne         = 1.0
)

// applyCap 在给定原始目标权重上施加单一个股权重上限 capFraction（如 0.1 表示 10%）。
//
// 求解一个统一的缩放系数 f >= 1，使封顶后的权重
//
//	p_i = min(f * w_i, c)
//
// 满足 Σp_i = 1。触顶成分恰好等于 c，未触顶成分按同一 f 等比放大，
// 因此调整后权重总和严格归一，成分之间的相对权重在未触顶部分保持不变。
// f 由二分法求解：g(f) = Σmin(f*w_i, c) 对 f 单调不减，故必然收敛。
// capFraction <= 0 或 >= 1 时不封顶，仅做归一化。
func applyCap(weights map[string]float64, capFraction float64) (map[string]float64, float64, error) {
	if len(weights) == 0 {
		return nil, 0, ErrEmptyWeights
	}
	symbols := make([]string, 0, len(weights))
	sum := 0.0
	for symbol, weight := range weights {
		if weight < 0 {
			return nil, 0, errors.New("negative weight for " + symbol)
		}
		symbols = append(symbols, symbol)
		sum += weight
	}
	sort.Strings(symbols)
	if sum <= 0 {
		return nil, 0, errors.New("non-positive total weight")
	}

	out := make(map[string]float64, len(weights))
	noCap := capFraction <= 0 || capFraction >= 1
	if noCap {
		for _, symbol := range symbols {
			out[symbol] = weights[symbol] / sum
		}
		return out, factorOne, nil
	}
	if capFraction*float64(len(symbols)) < 1-math.SmallestNonzeroFloat64 {
		return nil, 0, ErrCapInfeasible
	}

	normalized := make([]float64, len(symbols))
	for index, symbol := range symbols {
		normalized[index] = weights[symbol] / sum
	}

	// g(f) 单调不减；f=1 时 g<=1（有成分触顶则严格小于 1）。
	lo, hi := factorOne, factorOne
	for cappedSum(normalized, hi, capFraction) < factorOne {
		hi *= 2
		if math.IsInf(hi, 1) {
			return nil, 0, ErrCapInfeasible
		}
	}
	for iteration := 0; iteration < 200; iteration++ {
		mid := (lo + hi) / 2
		value := cappedSum(normalized, mid, capFraction)
		if math.Abs(value-factorOne) <= capConvergenceEps {
			lo, hi = mid, mid
			break
		}
		if value < factorOne {
			lo = mid
		} else {
			hi = mid
		}
	}
	factor := (lo + hi) / 2

	total := 0.0
	for index, raw := range normalized {
		capped := math.Min(factor*raw, capFraction)
		out[symbols[index]] = capped
		total += capped
	}
	// 抵消二分残差：整体等比微缩，保证总和在浮点意义下严格等于 1。
	for symbol := range out {
		out[symbol] /= total
	}
	return out, factor, nil
}

func cappedSum(normalized []float64, factor, capFraction float64) float64 {
	sum := 0.0
	for _, weight := range normalized {
		sum += math.Min(factor*weight, capFraction)
	}
	return sum
}
