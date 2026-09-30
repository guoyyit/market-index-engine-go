package index

import (
	"math"
	"testing"
	"time"
)

func assertLevelsClose(t *testing.T, got, want IndexValue, label string) {
	t.Helper()
	const eps = 1e-8
	if math.Abs(got.Price-want.Price) > eps {
		t.Fatalf("%s: price rebuild %v != live %v", label, got.Price, want.Price)
	}
	if math.Abs(got.TotalReturn-want.TotalReturn) > eps {
		t.Fatalf("%s: TR rebuild %v != live %v", label, got.TotalReturn, want.TotalReturn)
	}
}

// historyScenario 构造一段含价格变动、现金分红、送转、配股与成分调样的完整事件流，
// 返回已持久化的引擎目录与每个行情时点实时计算出的指数值。
func historyScenario(t *testing.T, capFraction float64) (string, []IndexValue) {
	t.Helper()
	dir := t.TempDir()
	engine, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer engine.Close()

	for _, symbol := range []string{"A", "B", "C", "D"} {
		if err := engine.RegisterInstrument(Instrument{Symbol: symbol}); err != nil {
			t.Fatalf("register %s: %v", symbol, err)
		}
	}
	base := time.Date(2026, 2, 2, 15, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return base.AddDate(0, 0, n) }

	if err := engine.Initialize(
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.3}, {Symbol: "C", Weight: 0.2}},
		[]Quote{
			{Symbol: "A", At: base, Price: 10},
			{Symbol: "B", At: base, Price: 20},
			{Symbol: "C", At: base, Price: 40},
		}); err != nil {
		t.Fatalf("init: %v", err)
	}

	live := []IndexValue{engine.Level()}

	// day1：纯价格变动。
	quotes(t, engine, day(1), []Quote{
		{Symbol: "A", At: day(1), Price: 11},
		{Symbol: "B", At: day(1), Price: 21},
		{Symbol: "C", At: day(1), Price: 38},
	})
	live = append(live, engine.Level())

	// day2：公告日（不改变状态）。
	if err := engine.RecordAction(CorporateAction{
		ID: "div-A", Symbol: "A", Kind: CashDividend,
		AnnouncedAt: day(2), ExDate: day(3), Dividend: 1,
	}); err != nil {
		t.Fatalf("dividend: %v", err)
	}
	if err := engine.RecordAction(CorporateAction{
		ID: "split-B", Symbol: "B", Kind: StockSplit,
		AnnouncedAt: day(2), ExDate: day(3), SplitFactor: 2,
	}); err != nil {
		t.Fatalf("split: %v", err)
	}
	if err := engine.RecordAction(CorporateAction{
		ID: "rights-C", Symbol: "C", Kind: RightsIssue,
		AnnouncedAt: day(2), ExDate: day(3),
		RightsRatio: 0.5, RightsPrice: 20,
	}); err != nil {
		t.Fatalf("rights: %v", err)
	}
	if err := engine.SubmitChange(ConstituentChange{
		ID:          "chg-1",
		Adds:        []Weight{{Symbol: "D", Weight: 0.2}},
		Removes:     []string{"C"},
		WeightPatch: map[string]float64{"A": 0.5, "B": 0.3},
		AnnouncedAt: day(2), EffectiveAt: day(5),
	}); err != nil {
		t.Fatalf("change: %v", err)
	}

	// day3：除权除息日。动作使用 day1 收盘价作为 cum 价，再给出除权价。
	terpC := (38 + 0.5*20) / 1.5
	quotes(t, engine, day(3), []Quote{
		{Symbol: "A", At: day(3), Price: 11 - 1},
		{Symbol: "B", At: day(3), Price: 21 / 2},
		{Symbol: "C", At: day(3), Price: terpC},
	})
	live = append(live, engine.Level())

	// day4：再一日价格变动。
	quotes(t, engine, day(4), []Quote{
		{Symbol: "A", At: day(4), Price: 11},
		{Symbol: "B", At: day(4), Price: 12},
		{Symbol: "C", At: day(4), Price: terpC},
	})
	live = append(live, engine.Level())

	// day5：调样生效（删 C、入 D）。
	quotes(t, engine, day(5), []Quote{
		{Symbol: "A", At: day(5), Price: 11},
		{Symbol: "B", At: day(5), Price: 12},
		{Symbol: "D", At: day(5), Price: 55},
	})
	live = append(live, engine.Level())

	// day6：生效后再一日。
	quotes(t, engine, day(6), []Quote{
		{Symbol: "A", At: day(6), Price: 12},
		{Symbol: "B", At: day(6), Price: 13},
		{Symbol: "D", At: day(6), Price: 50},
	})
	live = append(live, engine.Level())

	return dir, live
}

func quotes(t *testing.T, engine *Engine, at time.Time, list []Quote) {
	t.Helper()
	for _, quote := range list {
		if err := engine.ApplyQuote(quote); err != nil {
			t.Fatalf("quote %s @ %s: %v", quote.Symbol, at.Format("01-02"), err)
		}
	}
}

func TestHistoryMatchesLive(t *testing.T) {
	dir, live := historyScenario(t, 0)

	reopened, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	// 最新时点：重启恢复的实时值与原始实时值一致。
	assertLevelsClose(t, reopened.Level(), live[len(live)-1], "recovered latest")

	// 逐点重建：每个历史行情时点都必须与当时的实时值一致。
	for _, want := range live {
		got, err := reopened.RebuildAt(want.At)
		if err != nil {
			t.Fatalf("rebuild at %s: %v", want.At, err)
		}
		assertLevelsClose(t, got, want, want.At.Format("01-02"))
	}

	// 区间重建的点数与逐点结果一致。
	values, err := reopened.RebuildRange(live[0].At, live[len(live)-1].At)
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(values) != len(live) {
		t.Fatalf("range points = %d, want %d", len(values), len(live))
	}
	for index := range values {
		assertLevelsClose(t, values[index], live[index], "range")
	}
}

func TestHistoryMatchesLiveAfterSnapshot(t *testing.T) {
	dir, live := historyScenario(t, 0)

	checkpointed, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := checkpointed.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	checkpointed.Close()

	// 从快照恢复后，再次全历史逐点重建，结果必须仍然一致。
	rebuilt, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer rebuilt.Close()
	for _, want := range live {
		got, err := rebuilt.RebuildAt(want.At)
		if err != nil {
			t.Fatalf("rebuild after snapshot at %s: %v", want.At, err)
		}
		assertLevelsClose(t, got, want, want.At.Format("01-02"))
	}
}

func TestHistoryRebuildBeforeBase(t *testing.T) {
	dir, _ := historyScenario(t, 0)
	engine, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer engine.Close()
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := engine.RebuildAt(before); err != ErrNotInitialized {
		t.Fatalf("want ErrNotInitialized before base, got %v", err)
	}
}
