package index

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestPriceTRConvertibilityAcrossMixedActions(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	ticks := []struct {
		day    int
		prices map[string]float64
	}{
		{2, map[string]float64{"AAA": 10, "BBB": 20}},
		{3, map[string]float64{"AAA": 9, "BBB": 20}},   // div 1 ex
		{4, map[string]float64{"AAA": 4.5, "BBB": 20}}, // 2:1 split ex (ref 4.5)
		{5, map[string]float64{"AAA": 4, "BBB": 20}},   // normal move after split
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.RecordCorporateAction("IDX", CorporateAction{
		ID: "D1", Symbol: "AAA", Type: ActionCashDividend,
		ExDate: day(3), CashPerShare: 1,
	}))
	must(e.RecordCorporateAction("IDX", CorporateAction{
		ID: "S1", Symbol: "AAA", Type: ActionSplit,
		ExDate: day(4), SplitRatio: 2,
	}))
	for _, tk := range ticks {
		must(e.OnTick("IDX", day(tk.day), tk.prices))
	}

	points, err := e.Rebuild("IDX")
	if err != nil {
		t.Fatal(err)
	}
	// At every point TR/Price must equal the accumulated reinvestment factor,
	// i.e. the difference comes only from the reinvested dividend.
	for i, pt := range points {
		ratio := pt.TotalReturn / pt.Price
		want := 1.0
		if i >= 1 { // dividend reinvested on day 3
			want = 10.0 / 9.0
		}
		if math.Abs(ratio-want) > 1e-10 {
			t.Fatalf("point %d (%s): TR/Price = %.10f, want %.10f",
				i, pt.Time.Format("01-02"), ratio, want)
		}
	}

	// Split continuity at the theoretical ex price (day 4 == day 3 price level).
	if math.Abs(points[2].Price-points[1].Price) > 1e-9 {
		t.Fatalf("split broke continuity: %.8f -> %.8f", points[1].Price, points[2].Price)
	}
}

func TestRebuildRange(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)
	for n := 2; n <= 6; n++ {
		if err := e.OnTick("IDX", day(n), map[string]float64{"AAA": 10, "BBB": 20}); err != nil {
			t.Fatal(err)
		}
	}

	points, err := e.RebuildRange("IDX", day(3), day(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("range returned %d points, want 3", len(points))
	}
	if !points[0].Time.Equal(day(3)) || !points[2].Time.Equal(day(5)) {
		t.Fatalf("range bounds wrong: %s .. %s", points[0].Time, points[2].Time)
	}

	// Open-ended upper bound.
	tail, err := e.RebuildRange("IDX", day(5), time.Time{})
	if err != nil || len(tail) != 2 {
		t.Fatalf("open-ended range = %d points, err=%v", len(tail), err)
	}
}

func TestMultipleIndicesAreIsolated(t *testing.T) {
	e := newTestEngine(t)
	registerDemoIndex(t, e, 0)

	other := IndexDef{Code: "IDX2", Name: "Other", BaseDate: day(1), BaseValue: 100}
	if err := e.RegisterIndex(other,
		[]Constituent{{Symbol: "ZZZ", Weight: 1}},
		map[string]float64{"ZZZ": 50},
	); err != nil {
		t.Fatal(err)
	}

	if err := e.OnTick("IDX", day(2), map[string]float64{"AAA": 11, "BBB": 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.OnTick("IDX2", day(2), map[string]float64{"ZZZ": 55}); err != nil {
		t.Fatal(err)
	}

	p1, _ := e.PriceIndex("IDX")
	p2, _ := e.PriceIndex("IDX2")
	if math.Abs(p1-(0.6*11+0.4*20)/14*1000) > 1e-9 {
		t.Fatalf("IDX = %.6f", p1)
	}
	if math.Abs(p2-110) > 1e-9 {
		t.Fatalf("IDX2 = %.6f, want 110", p2)
	}

	points, err := e.Rebuild("IDX2")
	if err != nil || len(points) != 1 || math.Abs(points[0].Price-110) > 1e-9 {
		t.Fatalf("IDX2 rebuild wrong: %+v err=%v", points, err)
	}
}

func TestUnknownIndexAndBadInput(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.PriceIndex("NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := e.RegisterIndex(IndexDef{Code: "", BaseValue: 1000},
		[]Constituent{{Symbol: "A", Weight: 1}}, nil); err == nil {
		t.Fatal("expected error for empty code")
	}
	registerDemoIndex(t, e, 0)
	if err := e.RegisterIndex(IndexDef{Code: "IDX", BaseValue: 1000},
		[]Constituent{{Symbol: "A", Weight: 1}}, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
}
