package index

import (
	"math"
	"testing"
)

func TestApplyCapBasic(t *testing.T) {
	// 40/30/20/10，25% 封顶：全部触顶 => 25% 每个（4 个成分的可行下限即 25%）。
	weights := map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2, "D": 0.1}
	capped, factor, err := applyCap(weights, 0.25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if factor < 1 {
		t.Fatalf("factor must be >= 1, got %v", factor)
	}
	sum := 0.0
	for _, weight := range capped {
		sum += weight
		if math.Abs(weight-0.25) > 1e-9 {
			t.Fatalf("expected every weight 0.25, got %v", weight)
		}
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("weights must sum to 1, got %v", sum)
	}
}

func TestApplyCapPartial(t *testing.T) {
	// 30/20/20/20/10，25% 封顶：A 触顶 25%，其余等比放大到合计 75%。
	weights := map[string]float64{
		"A": 0.30, "B": 0.20, "C": 0.20, "D": 0.20, "E": 0.10,
	}
	capped, factor, err := applyCap(weights, 0.25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(capped["A"]-0.25) > 1e-9 {
		t.Fatalf("A must be capped at 0.25, got %v", capped["A"])
	}
	// 未触顶成分相对比例不变，f 统一放大。
	if math.Abs(capped["B"]-capped["C"]) > 1e-9 || math.Abs(capped["C"]-capped["D"]) > 1e-9 {
		t.Fatalf("B/C/D must be scaled equally: %v", capped)
	}
	if math.Abs(capped["B"]/capped["E"]-2) > 1e-9 {
		t.Fatalf("B/E relative weight must stay 2: %v / %v", capped["B"], capped["E"])
	}
	sum := 0.0
	for _, weight := range capped {
		if weight > 0.25+1e-12 {
			t.Fatalf("weight exceeds cap: %v", weight)
		}
		sum += weight
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("weights must sum to 1, got %v", sum)
	}
	if factor <= 1 {
		t.Fatalf("factor should be > 1 when cap binds, got %v", factor)
	}
}

func TestApplyCapNoBinding(t *testing.T) {
	weights := map[string]float64{"A": 0.3, "B": 0.7}
	capped, factor, err := applyCap(weights, 0.8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if factor != 1 {
		t.Fatalf("factor should be 1 when cap does not bind, got %v", factor)
	}
	if math.Abs(capped["A"]-0.3) > 1e-12 || math.Abs(capped["B"]-0.7) > 1e-12 {
		t.Fatalf("weights should be unchanged: %v", capped)
	}
}

func TestApplyCapNormalizesRawInput(t *testing.T) {
	weights := map[string]float64{"A": 60, "B": 40}
	capped, _, err := applyCap(weights, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(capped["A"]-0.6) > 1e-12 || math.Abs(capped["B"]-0.4) > 1e-12 {
		t.Fatalf("raw weights must normalize: %v", capped)
	}
}

func TestApplyCapInfeasible(t *testing.T) {
	weights := map[string]float64{"A": 0.6, "B": 0.4}
	if _, _, err := applyCap(weights, 0.1); err != ErrCapInfeasible {
		t.Fatalf("expected ErrCapInfeasible, got %v", err)
	}
	if _, _, err := applyCap(nil, 0.1); err != ErrEmptyWeights {
		t.Fatalf("expected ErrEmptyWeights, got %v", err)
	}
}
