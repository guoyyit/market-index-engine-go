package index

import (
	"math"
	"testing"
	"time"
)

func mustInitEngine(t *testing.T, capFraction float64, weights []Weight, baseAt string) *Engine {
	t.Helper()
	engine := NewEngine(capFraction)
	for _, entry := range weights {
		if err := engine.RegisterInstrument(Instrument{Symbol: entry.Symbol, Name: entry.Symbol}); err != nil {
			t.Fatalf("register %s: %v", entry.Symbol, err)
		}
	}
	at := parseTime(t, baseAt)
	quotes := make([]Quote, 0, len(weights))
	for _, entry := range weights {
		quotes = append(quotes, Quote{Symbol: entry.Symbol, At: at, Price: basePrice(entry.Symbol)})
	}
	if err := engine.Initialize(weights, quotes); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return engine
}

func basePrice(symbol string) float64 {
	switch symbol {
	case "A":
		return 10
	case "B":
		return 20
	case "C":
		return 40
	default:
		return 15
	}
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return at
}

func TestEngineBaseAndMove(t *testing.T) {
	engine := mustInitEngine(t, 0,
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}},
		"2026-01-02T15:00:00Z")
	level := engine.Level()
	if math.Abs(level.Price-DefaultBaseLevel) > 1e-9 ||
		math.Abs(level.TotalReturn-DefaultBaseLevel) > 1e-9 {
		t.Fatalf("base levels = %+v", level)
	}

	if err := engine.ApplyQuote(Quote{
		Symbol: "A", At: parseTime(t, "2026-01-03T15:00:00Z"), Price: 11,
	}); err != nil {
		t.Fatalf("quote: %v", err)
	}
	level = engine.Level()
	want := DefaultBaseLevel * (1 + 0.5*0.1)
	if math.Abs(level.Price-want) > 1e-9 || math.Abs(level.TotalReturn-want) > 1e-9 {
		t.Fatalf("after move levels = %+v, want price %v", level, want)
	}
}

func TestEngineReweightNoJump(t *testing.T) {
	engine := mustInitEngine(t, 0,
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}},
		"2026-01-02T15:00:00Z")
	engine.RegisterInstrument(Instrument{Symbol: "C"})

	changeAt := parseTime(t, "2026-01-05T15:00:00Z")
	change := ConstituentChange{
		ID:          "chg-1",
		Adds:        []Weight{{Symbol: "C", Weight: 1.0 / 3}},
		WeightPatch: map[string]float64{"A": 1.0 / 3, "B": 1.0 / 3},
		AnnouncedAt: parseTime(t, "2026-01-04T09:00:00Z"),
		EffectiveAt: changeAt,
	}
	if err := engine.SubmitChange(change); err != nil {
		t.Fatalf("submit change: %v", err)
	}

	// 生效日前仍按旧权重。
	pre := parseTime(t, "2026-01-04T15:00:00Z")
	for _, quote := range []Quote{
		{Symbol: "A", At: pre, Price: 12},
		{Symbol: "B", At: pre, Price: 22},
	} {
		if err := engine.ApplyQuote(quote); err != nil {
			t.Fatalf("quote: %v", err)
		}
	}
	before := engine.Level()

	// 生效日首条行情触发原子切换；新成分 C 的价格在切换时必须可得。
	if err := engine.ApplyQuote(Quote{Symbol: "C", At: changeAt, Price: 33}); err != nil {
		t.Fatalf("quote C: %v", err)
	}
	after := engine.Level()
	if math.Abs(after.Price-before.Price) > 1e-9 || math.Abs(after.TotalReturn-before.TotalReturn) > 1e-9 {
		t.Fatalf("reweight jumped: before=%+v after=%+v", before, after)
	}
	constituents := engine.Constituents()
	if len(constituents) != 3 {
		t.Fatalf("constituents = %v", constituents)
	}
	sum := 0.0
	for _, weight := range constituents {
		sum += weight
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights sum = %v", sum)
	}
}

func TestEngineCashDividendGap(t *testing.T) {
	engine := mustInitEngine(t, 0,
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}},
		"2026-01-02T15:00:00Z")
	exDate := parseTime(t, "2026-01-06T15:00:00Z")
	action := CorporateAction{
		ID:          "div-1",
		Symbol:      "A",
		Kind:        CashDividend,
		AnnouncedAt: parseTime(t, "2026-01-05T09:00:00Z"),
		ExDate:      exDate,
		Dividend:    1,
	}
	if err := engine.RecordAction(action); err != nil {
		t.Fatalf("record action: %v", err)
	}

	// 除息日：登记含权收盘价 10（A 仍是基期价 10），随后提供除息价 9。
	cumClose := parseTime(t, "2026-01-05T15:00:00Z")
	if err := engine.ApplyQuote(Quote{Symbol: "A", At: cumClose, Price: 10}); err != nil {
		t.Fatalf("cum quote: %v", err)
	}
	if err := engine.ApplyQuote(Quote{Symbol: "A", At: exDate, Price: 9}); err != nil {
		t.Fatalf("ex quote: %v", err)
	}

	level := engine.Level()
	// 价格指数：A 跌 10%、权重 50% => 跌 5%。
	wantPrice := DefaultBaseLevel * 0.95
	// 全收益指数：除息再投资 => 持平。
	if math.Abs(level.Price-wantPrice) > 1e-9 {
		t.Fatalf("price level = %v, want %v", level.Price, wantPrice)
	}
	if math.Abs(level.TotalReturn-DefaultBaseLevel) > 1e-9 {
		t.Fatalf("TR level = %v, want %v", level.TotalReturn, DefaultBaseLevel)
	}
	if factor := engine.ReinvestmentFactor(); math.Abs(factor-level.TotalReturn/level.Price) > 1e-9 {
		t.Fatalf("reinvestment factor %v != TR/price %v", factor, level.TotalReturn/level.Price)
	}

	// 公司行为不得重复应用。
	if err := engine.RecordAction(action); err == nil {
		t.Fatal("duplicate action must be rejected")
	}
}

func TestEngineCapAppliedOnReweight(t *testing.T) {
	// 初始 3 只；调样把 A 目标权重设到 60%，50% 封顶生效（仅 A 触顶）。
	engine := mustInitEngine(t, 0.5,
		[]Weight{{Symbol: "A", Weight: 1.0 / 3}, {Symbol: "B", Weight: 1.0 / 3}, {Symbol: "C", Weight: 1.0 / 3}},
		"2026-01-02T15:00:00Z")
	changeAt := parseTime(t, "2026-01-08T15:00:00Z")
	err := engine.SubmitChange(ConstituentChange{
		ID:          "chg-cap",
		WeightPatch: map[string]float64{"A": 0.6, "B": 0.3, "C": 0.1},
		AnnouncedAt: parseTime(t, "2026-01-07T09:00:00Z"),
		EffectiveAt: changeAt,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := engine.ApplyQuote(Quote{Symbol: "A", At: changeAt, Price: 10}); err != nil {
		t.Fatalf("quote A: %v", err)
	}
	weights := engine.Constituents()
	if math.Abs(weights["A"]-0.5) > 1e-9 {
		t.Fatalf("A must be capped at 0.5, got %v", weights["A"])
	}
	// B/C 未触顶，相对比例保持 3:1。
	if math.Abs(weights["B"]/weights["C"]-3) > 1e-9 {
		t.Fatalf("B/C relative ratio must stay 3: %v", weights)
	}
	for symbol, weight := range weights {
		if weight > 0.5+1e-9 {
			t.Fatalf("%s weight %v exceeds cap", symbol, weight)
		}
	}
	sum := 0.0
	for _, weight := range weights {
		sum += weight
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("capped weights sum = %v", sum)
	}
}

func TestEngineRejectOutOfOrderQuote(t *testing.T) {
	engine := mustInitEngine(t, 0,
		[]Weight{{Symbol: "A", Weight: 1}},
		"2026-01-02T15:00:00Z")
	err := engine.ApplyQuote(Quote{
		Symbol: "A", At: parseTime(t, "2026-01-01T15:00:00Z"), Price: 11,
	})
	if err != ErrOutOfOrderQuote {
		t.Fatalf("want ErrOutOfOrderQuote, got %v", err)
	}
}

func TestEngineConstituentRemoval(t *testing.T) {
	engine := mustInitEngine(t, 0,
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}},
		"2026-01-02T15:00:00Z")
	changeAt := parseTime(t, "2026-01-09T15:00:00Z")
	if err := engine.SubmitChange(ConstituentChange{
		ID:          "chg-rm",
		Removes:     []string{"B"},
		WeightPatch: map[string]float64{"A": 1},
		AnnouncedAt: parseTime(t, "2026-01-08T09:00:00Z"),
		EffectiveAt: changeAt,
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	before := engine.Level()
	if err := engine.ApplyQuote(Quote{Symbol: "A", At: changeAt, Price: 10}); err != nil {
		t.Fatalf("quote: %v", err)
	}
	after := engine.Level()
	if math.Abs(after.Price-before.Price) > 1e-9 || math.Abs(after.TotalReturn-before.TotalReturn) > 1e-9 {
		t.Fatalf("removal jumped: before=%+v after=%+v", before, after)
	}
	if len(engine.Constituents()) != 1 {
		t.Fatalf("B must be removed: %v", engine.Constituents())
	}
}
