package index

import (
	"math"
	"testing"
)

func TestSeriesBaseInit(t *testing.T) {
	series := newSeries(0)
	weights := map[string]float64{"A": 0.6, "B": 0.4}
	prices := map[string]float64{"A": 10, "B": 20}
	if err := series.initAtBase(weights, prices, DefaultBaseLevel); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := series.level(prices); math.Abs(got-DefaultBaseLevel) > 1e-9 {
		t.Fatalf("base level = %v, want %v", got, DefaultBaseLevel)
	}
}

func TestSeriesReweightContinuity(t *testing.T) {
	series := newSeries(0)
	weights := map[string]float64{"A": 0.6, "B": 0.4}
	prices := map[string]float64{"A": 10, "B": 20}
	if err := series.initAtBase(weights, prices, DefaultBaseLevel); err != nil {
		t.Fatalf("init: %v", err)
	}

	// 价格先上涨，再调样为 A/B/C 等权；切换瞬间点位不得跳变。
	prices = map[string]float64{"A": 12, "B": 19, "C": 30}
	before := series.level(map[string]float64{"A": 12, "B": 19})
	newWeights := map[string]float64{"A": 1.0 / 3, "B": 1.0 / 3, "C": 1.0 / 3}
	if err := series.reweight(newWeights, prices); err != nil {
		t.Fatalf("reweight: %v", err)
	}
	after := series.level(prices)
	if math.Abs(after-before) > 1e-9 {
		t.Fatalf("reweight caused jump: before=%v after=%v", before, after)
	}

	// 切换后价格变动正常反映：C 涨 10%，指数约涨 1/3 * 10%。
	prices["C"] = 33
	expected := after * (1 + (1.0/3)*0.1)
	if got := series.level(prices); math.Abs(got-expected) > 1e-9 {
		t.Fatalf("post-reweight move: got %v want %v", got, expected)
	}
}

func TestSeriesReweightMissingPrice(t *testing.T) {
	series := newSeries(1)
	series.shares = map[string]float64{"A": 1}
	if err := series.reweight(map[string]float64{"A": 0.5, "B": 0.5},
		map[string]float64{"A": 10}); err == nil {
		t.Fatal("expected error when new constituent lacks price")
	}
}
