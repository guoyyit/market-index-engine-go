package index

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func day(n int) time.Time {
	return time.Date(2025, 1, n, 15, 0, 0, 0, time.UTC)
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func registerDemoIndex(t *testing.T, e *Engine, weightCap float64) {
	t.Helper()
	def := IndexDef{
		Code:      "IDX",
		Name:      "Demo",
		BaseDate:  day(1),
		BaseValue: 1000,
		WeightCap: weightCap,
	}
	base := []Constituent{
		{Symbol: "AAA", Weight: 0.6},
		{Symbol: "BBB", Weight: 0.4},
	}
	prices := map[string]float64{"AAA": 10, "BBB": 20}
	if err := e.RegisterIndex(def, base, prices); err != nil {
		t.Fatalf("RegisterIndex: %v", err)
	}
}

func TestBaseLevel(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	p, err := e.PriceIndex("IDX")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(p-1000) > 1e-9 {
		t.Fatalf("base level = %.6f, want 1000", p)
	}
	tr, _ := e.TotalReturnIndex("IDX")
	if math.Abs(tr-1000) > 1e-9 {
		t.Fatalf("TR base level = %.6f, want 1000", tr)
	}
}

func TestCashDividendOnlyAffectsTotalReturn(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordCorporateAction("IDX", CorporateAction{
		ID: "D1", Symbol: "AAA", Type: ActionCashDividend,
		AnnounceDate: day(2), ExDate: day(3), CashPerShare: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 9, "BBB": 20}); err != nil {
		t.Fatal(err)
	}

	p, _ := e.PriceIndex("IDX")
	if want := 13.4 / 14 * 1000; math.Abs(p-want) > 1e-6 {
		t.Fatalf("price = %.6f, want %.6f", p, want)
	}
	tr, _ := e.TotalReturnIndex("IDX")
	if want := p * 10 / 9; math.Abs(tr-want) > 1e-6 {
		t.Fatalf("TR = %.6f, want %.6f", tr, want)
	}
}

func TestSplitKeepsLevelContinuous(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordCorporateAction("IDX", CorporateAction{
		ID: "S1", Symbol: "AAA", Type: ActionSplit,
		AnnounceDate: day(2), ExDate: day(3), SplitRatio: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 5, "BBB": 20}); err != nil {
		t.Fatal(err)
	}

	p, _ := e.PriceIndex("IDX")
	if math.Abs(p-1000) > 1e-6 {
		t.Fatalf("price after split at ref price = %.6f, want 1000", p)
	}
}

func TestRightsIssueKeepsLevelContinuous(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordCorporateAction("IDX", CorporateAction{
		ID: "R1", Symbol: "AAA", Type: ActionRightsIssue,
		AnnounceDate: day(2), ExDate: day(3),
		RightsRatio: 1, RightsPrice: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 7, "BBB": 20}); err != nil {
		t.Fatal(err)
	}

	p, _ := e.PriceIndex("IDX")
	if math.Abs(p-1000) > 1e-6 {
		t.Fatalf("price after rights at ref price = %.6f, want 1000", p)
	}
}

func TestRebalanceContinuityAndAtomicity(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.AnnounceRebalance("IDX", Rebalance{
		ID: "RB1", AnnounceDate: day(2), EffectiveDate: day(4),
		Constituents: []Constituent{
			{Symbol: "AAA", Weight: 0.5},
			{Symbol: "CCC", Weight: 0.5},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 11, "BBB": 20, "CCC": 50}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.PriceIndex("IDX")
	cons, _ := e.CurrentConstituents("IDX")
	if len(cons) != 2 || cons[0].Symbol != "AAA" || cons[1].Symbol != "BBB" {
		t.Fatalf("old basket must remain before effective date: %+v", cons)
	}

	if err := e.OnTick("IDX", day(4), map[string]float64{"AAA": 11, "BBB": 20, "CCC": 50}); err != nil {
		t.Fatal(err)
	}
	after, _ := e.PriceIndex("IDX")
	if math.Abs(after-before) > 1e-9 {
		t.Fatalf("rebalance caused a jump: %.8f -> %.8f", before, after)
	}
	cons, _ = e.CurrentConstituents("IDX")
	if len(cons) != 2 || cons[0].Symbol != "AAA" || cons[1].Symbol != "CCC" {
		t.Fatalf("new basket must be active: %+v", cons)
	}
}

func TestRebuildMatchesLive(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordCorporateAction("IDX", CorporateAction{
		ID: "D1", Symbol: "AAA", Type: ActionCashDividend,
		ExDate: day(3), CashPerShare: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 9, "BBB": 21}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(4), map[string]float64{"AAA": 9.5, "BBB": 20.5}); err != nil {
		t.Fatal(err)
	}

	points, err := e.Rebuild("IDX")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("rebuilt %d points, want 3", len(points))
	}
	liveP, _ := e.PriceIndex("IDX")
	liveTR, _ := e.TotalReturnIndex("IDX")
	last := points[len(points)-1]
	if math.Abs(last.Price-liveP) > 1e-12 {
		t.Fatalf("rebuilt price %.10f != live %.10f", last.Price, liveP)
	}
	if math.Abs(last.TotalReturn-liveTR) > 1e-12 {
		t.Fatalf("rebuilt TR %.10f != live %.10f", last.TotalReturn, liveTR)
	}

	at, err := e.RebuildAt("IDX", day(3).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !at.Time.Equal(day(3)) {
		t.Fatalf("RebuildAt picked %s, want day 3 tick", at.Time)
	}
	if math.Abs(at.Price-points[1].Price) > 1e-12 {
		t.Fatalf("RebuildAt price %.10f != %.10f", at.Price, points[1].Price)
	}
}

func TestRestartRecovery(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")

	e, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	registerDemoIndex(t, e, 0)
	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordCorporateAction("IDX", CorporateAction{
		ID: "S1", Symbol: "AAA", Type: ActionSplit,
		ExDate: day(3), SplitRatio: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX", day(3), map[string]float64{"AAA": 5, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	beforeP, _ := e.PriceIndex("IDX")
	beforeTR, _ := e.TotalReturnIndex("IDX")
	cons, _ := e.CurrentConstituents("IDX")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	afterP, _ := e2.PriceIndex("IDX")
	afterTR, _ := e2.TotalReturnIndex("IDX")
	if math.Abs(afterP-beforeP) > 1e-12 || math.Abs(afterTR-beforeTR) > 1e-12 {
		t.Fatalf("levels changed across restart: %.10f/%.10f -> %.10f/%.10f",
			beforeP, beforeTR, afterP, afterTR)
	}
	cons2, _ := e2.CurrentConstituents("IDX")
	if len(cons2) != len(cons) || math.Abs(cons2[0].Weight-cons[0].Weight) > 1e-12 {
		t.Fatalf("basket weights not recovered: %+v vs %+v", cons2, cons)
	}

	// A later flat tick must not re-apply the already applied split.
	if err := e2.OnTick("IDX", day(4), map[string]float64{"AAA": 5, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	p, _ := e2.PriceIndex("IDX")
	if math.Abs(p-beforeP) > 1e-9 {
		t.Fatalf("level changed on post-restart flat tick: %.8f vs %.8f", p, beforeP)
	}
}

func TestScheduledRebalanceSurvivesRestart(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")

	e, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	registerDemoIndex(t, e, 0)
	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.AnnounceRebalance("IDX", Rebalance{
		ID: "RB1", AnnounceDate: day(2), EffectiveDate: day(5),
		Constituents: []Constituent{
			{Symbol: "AAA", Weight: 0.5},
			{Symbol: "CCC", Weight: 0.5},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if err := e2.OnTick("IDX", day(5), map[string]float64{"AAA": 10, "BBB": 20, "CCC": 30}); err != nil {
		t.Fatal(err)
	}
	cons, _ := e2.CurrentConstituents("IDX")
	if len(cons) != 2 || cons[1].Symbol != "CCC" {
		t.Fatalf("scheduled rebalance not applied after restart: %+v", cons)
	}
}

func TestWeightCapOnRegistrationAndRebalance(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0.5)

	cons, _ := e.CurrentConstituents("IDX")
	var sum float64
	for _, c := range cons {
		sum += c.Weight
		if c.Weight > 0.5+1e-9 {
			t.Fatalf("base weight %.4f exceeds cap", c.Weight)
		}
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("base weights sum = %.6f", sum)
	}

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.PriceIndex("IDX")
	if math.Abs(p-1000) > 1e-6 {
		t.Fatalf("base level with cap = %.6f, want 1000", p)
	}
}
