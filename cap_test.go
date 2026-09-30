package index

import (
	"math"
	"testing"
)

func TestApplyWeightCap(t *testing.T) {
	cons := []Constituent{
		{Symbol: "A", Weight: 0.50},
		{Symbol: "B", Weight: 0.30},
		{Symbol: "C", Weight: 0.15},
		{Symbol: "D", Weight: 0.05},
	}
	got := applyWeightCap(cons, 0.30)

	var sum float64
	for _, c := range got {
		sum += c.Weight
		if c.Weight > 0.30+capConvergenceTolerance {
			t.Fatalf("weight %.6f exceeds cap", c.Weight)
		}
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("weights sum to %.12f, want 1", sum)
	}
	if math.Abs(got[0].Weight-0.30) > 1e-9 || math.Abs(got[1].Weight-0.30) > 1e-9 {
		t.Fatalf("A/B = %.6f/%.6f, want capped 0.30/0.30", got[0].Weight, got[1].Weight)
	}
	if math.Abs(got[3].Weight-0.10) > 1e-9 {
		t.Fatalf("D = %.6f, want 0.10", got[3].Weight)
	}
}

func TestApplyWeightCapNoCapNeeded(t *testing.T) {
	cons := []Constituent{
		{Symbol: "A", Weight: 0.60},
		{Symbol: "B", Weight: 0.40},
	}
	got := applyWeightCap(cons, 0.90)
	var sum float64
	for _, c := range got {
		sum += c.Weight
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("sum = %v", sum)
	}
}
