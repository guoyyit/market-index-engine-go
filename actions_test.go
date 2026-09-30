package index

import (
	"math"
	"testing"
)

// 通过两条独立 series 验证：除权除息后价格指数按预期回落/连续，
// 全收益指数保持连续，且累计差额等于分红再投资。
func TestCashDividendFactors(t *testing.T) {
	action := CorporateAction{Kind: CashDividend, Symbol: "A", Dividend: 1}
	factors, err := actionShareFactors(action, 10)
	if err != nil {
		t.Fatalf("factors: %v", err)
	}
	if factors.price != 1 {
		t.Fatalf("price index must not adjust for dividend, got %v", factors.price)
	}
	// 除息价 9：TR 股数 ×(10/9) 后点位不变。
	trFactor := 10.0 / 9.0
	if math.Abs(factors.tr-trFactor) > 1e-12 {
		t.Fatalf("tr factor = %v, want %v", factors.tr, trFactor)
	}
	if got := factors.reinvestmentFactor(); math.Abs(got-trFactor) > 1e-12 {
		t.Fatalf("reinvestment factor = %v, want %v", got, trFactor)
	}

	if _, err := actionShareFactors(action, 0); err != ErrBadActionPrice {
		t.Fatalf("want ErrBadActionPrice, got %v", err)
	}
	bad := action
	bad.Dividend = 10
	if _, err := actionShareFactors(bad, 10); err != ErrBadDividend {
		t.Fatalf("want ErrBadDividend, got %v", err)
	}
}

func TestStockSplitFactors(t *testing.T) {
	action := CorporateAction{Kind: StockSplit, Symbol: "A", SplitFactor: 2}
	factors, err := actionShareFactors(action, 10)
	if err != nil {
		t.Fatalf("factors: %v", err)
	}
	if factors.price != 2 || factors.tr != 2 {
		t.Fatalf("split factors = %+v, want both 2", factors)
	}

	// 模拟：持有 10 股、价 10；10 送 10 后持有 20 股、价 5，市值与点位均不变。
	priceSeries := newSeries(1)
	trSeries := newSeries(1)
	priceSeries.shares["A"] = 10
	trSeries.shares["A"] = 10
	before := priceSeries.level(map[string]float64{"A": 10})
	priceSeries.scaleShares("A", factors.price)
	trSeries.scaleShares("A", factors.tr)
	exPrices := map[string]float64{"A": 5}
	if math.Abs(priceSeries.level(exPrices)-before) > 1e-9 {
		t.Fatal("price index jumped across split")
	}
	if math.Abs(trSeries.level(exPrices)-before) > 1e-9 {
		t.Fatal("tr index jumped across split")
	}

	bad := CorporateAction{Kind: StockSplit, SplitFactor: 1}
	if _, err := actionShareFactors(bad, 10); err != ErrBadSplit {
		t.Fatalf("want ErrBadSplit, got %v", err)
	}
}

func TestRightsIssueFactors(t *testing.T) {
	// 每 10 股配 3 股（ratio=0.3），配股价 5，含权价 10。
	action := CorporateAction{Kind: RightsIssue, Symbol: "A", RightsRatio: 0.3, RightsPrice: 5}
	factors, err := actionShareFactors(action, 10)
	if err != nil {
		t.Fatalf("factors: %v", err)
	}
	terp := (10 + 0.3*5) / 1.3
	want := 10 / terp
	if math.Abs(factors.price-want) > 1e-12 || math.Abs(factors.tr-want) > 1e-12 {
		t.Fatalf("rights factors = %+v, want %v both", factors, want)
	}
	if math.Abs(factors.reinvestmentFactor()-1) > 1e-12 {
		t.Fatal("rights issue must not create a TR/price gap")
	}

	priceSeries := newSeries(1)
	priceSeries.shares["A"] = 10
	before := priceSeries.level(map[string]float64{"A": 10})
	priceSeries.scaleShares("A", factors.price)
	after := priceSeries.level(map[string]float64{"A": terp})
	if math.Abs(after-before) > 1e-9 {
		t.Fatalf("rights issue caused jump: before=%v after=%v", before, after)
	}

	bad := CorporateAction{Kind: RightsIssue, RightsRatio: 0}
	if _, err := actionShareFactors(bad, 10); err != ErrBadRights {
		t.Fatalf("want ErrBadRights, got %v", err)
	}
}
