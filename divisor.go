package index

// weightedSum computes Σ weight_i * price_i over the constituents.
func weightedSum(cons []Constituent, prices map[string]float64) float64 {
	var sum float64
	for _, c := range cons {
		if p, ok := prices[c.Symbol]; ok {
			sum += c.Weight * p
		}
	}
	return sum
}

// adjustDivisor keeps the index level continuous when the raw sum moves
// from oldSum to newSum at the same instant.
func adjustDivisor(oldDivisor, oldSum, newSum float64) float64 {
	// Continuity condition: oldSum/oldDivisor == newSum/newDivisor.
	if oldSum == 0 {
		return oldDivisor
	}
	return oldDivisor * newSum / oldSum
}

// priceLevel evaluates the price index: rawSum / divisor.
func priceLevel(cons []Constituent, prices map[string]float64, divisor float64) float64 {
	if divisor == 0 {
		return 0
	}
	return weightedSum(cons, prices) / divisor
}
