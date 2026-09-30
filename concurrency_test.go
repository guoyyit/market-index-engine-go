package index

import (
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestConcurrentReadsAndWrites 在持续行情写入、调样登记与并发查询混合时，
// 借助 -race 验证无数据竞争，且任意查询读到的权重总和恒为 1（无半成品状态）。
func TestConcurrentReadsAndWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "engine")
	engine, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer engine.Close()

	const n = 6
	for index := 0; index < n; index++ {
		symbol := symbolName(index)
		if err := engine.RegisterInstrument(Instrument{Symbol: symbol}); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	base := time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC)
	weights := make([]Weight, n)
	quotes := make([]Quote, n)
	for index := range weights {
		weights[index] = Weight{Symbol: symbolName(index), Weight: 1.0 / n}
		quotes[index] = Quote{Symbol: symbolName(index), At: base, Price: 10 + float64(index)}
	}
	if err := engine.Initialize(weights, quotes); err != nil {
		t.Fatalf("init: %v", err)
	}

	stop := make(chan struct{})
	var writers sync.WaitGroup

	// 写入方：逐日推进多只成分行情。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for day := 1; day <= 30; day++ {
			select {
			case <-stop:
				return
			default:
			}
			at := base.AddDate(0, 0, day)
			for index := 0; index < n; index++ {
				price := 10 + float64(index) + float64(day%5)
				if err := engine.ApplyQuote(Quote{
					Symbol: symbolName(index), At: at, Price: price,
				}); err != nil {
					t.Errorf("quote day %d: %v", day, err)
					return
				}
			}
		}
	}()

	// 登记方：不断提交未来生效的调样单（但不使其生效，避免干扰写入）。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for day := 40; day < 60; day++ {
			select {
			case <-stop:
				return
			default:
			}
			patch := map[string]float64{}
			for index := 0; index < n; index++ {
				patch[symbolName(index)] = 1.0 / n
			}
			_ = engine.SubmitChange(ConstituentChange{
				ID:          changeName(day),
				WeightPatch: patch,
				AnnouncedAt: base.AddDate(0, 0, day),
				EffectiveAt: base.AddDate(0, 0, day+1),
			})
		}
	}()

	// 读取方：并发查询指数值与成分权重。
	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				level := engine.Level()
				if level.Price < 0 || level.TotalReturn < level.Price*(1-1e-9) {
					t.Errorf("implausible level: %+v", level)
					return
				}
				constituents := engine.Constituents()
				sum := 0.0
				for _, weight := range constituents {
					sum += weight
				}
				if len(constituents) > 0 && math.Abs(sum-1) > 1e-8 {
					t.Errorf("read mixed constituent state, sum=%v", sum)
					return
				}
				_ = engine.ReinvestmentFactor()
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	// 最终 TR 不低于价格指数（仅有上涨样本与无分红场景下相等）。
	final := engine.Level()
	if final.TotalReturn < final.Price*(1-1e-9) {
		t.Fatalf("TR below price: %+v", final)
	}
}

// TestRestartRecovery 在写入中途关闭并重新打开引擎：
// 已生效的调整与已登记的未来事件必须保留，且不得重复应用。
func TestRestartRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "engine")
	engine, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, symbol := range []string{"A", "B", "C"} {
		if err := engine.RegisterInstrument(Instrument{Symbol: symbol}); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	base := time.Date(2026, 4, 1, 15, 0, 0, 0, time.UTC)
	if err := engine.Initialize(
		[]Weight{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}},
		[]Quote{
			{Symbol: "A", At: base, Price: 10},
			{Symbol: "B", At: base, Price: 20},
		}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// 登记一个尚未生效的调样与一个尚未除息的分红。
	engine.RegisterInstrument(Instrument{Symbol: "C"})
	futureChange := ConstituentChange{
		ID:          "future",
		Adds:        []Weight{{Symbol: "C", Weight: 0.25}},
		WeightPatch: map[string]float64{"A": 0.5, "B": 0.25},
		AnnouncedAt: base.AddDate(0, 0, 2),
		EffectiveAt: base.AddDate(0, 0, 5),
	}
	if err := engine.SubmitChange(futureChange); err != nil {
		t.Fatalf("change: %v", err)
	}
	if err := engine.RecordAction(CorporateAction{
		ID:          "future-div",
		Symbol:      "A",
		Kind:        CashDividend,
		AnnouncedAt: base.AddDate(0, 0, 2),
		ExDate:      base.AddDate(0, 0, 6),
		Dividend:    1,
	}); err != nil {
		t.Fatalf("action: %v", err)
	}

	// 已生效的分红：第 3 天除息，重启发生在第 4 天。
	if err := engine.RecordAction(CorporateAction{
		ID:          "applied-div",
		Symbol:      "B",
		Kind:        CashDividend,
		AnnouncedAt: base,
		ExDate:      base.AddDate(0, 0, 3),
		Dividend:    2,
	}); err != nil {
		t.Fatalf("action: %v", err)
	}
	day3 := base.AddDate(0, 0, 3)
	for _, quote := range []Quote{
		{Symbol: "A", At: day3, Price: 10},
		{Symbol: "B", At: day3, Price: 18},
	} {
		if err := engine.ApplyQuote(quote); err != nil {
			t.Fatalf("quote: %v", err)
		}
	}
	beforeClose := engine.Level()
	if err := engine.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := OpenEngine(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	// 已生效状态恢复一致。
	afterOpen := reopened.Level()
	if math.Abs(afterOpen.Price-beforeClose.Price) > 1e-9 ||
		math.Abs(afterOpen.TotalReturn-beforeClose.TotalReturn) > 1e-9 {
		t.Fatalf("recovery mismatch: %+v vs %+v", beforeClose, afterOpen)
	}
	// 已应用的公司行为不可重复登记。
	if err := reopened.RecordAction(CorporateAction{
		ID: "applied-div", Symbol: "B", Kind: CashDividend,
		AnnouncedAt: base, ExDate: day3, Dividend: 2,
	}); err == nil {
		t.Fatal("already-applied action must be rejected after restart")
	}
	// 未生效的调样仍在等待，并在生效日正常切换。
	if _, ok := reopened.state.pendingChanges["future"]; !ok {
		t.Fatal("pending change lost across restart")
	}
	if _, ok := reopened.state.pendingActions["future-div"]; !ok {
		t.Fatal("pending action lost across restart")
	}

	day5 := base.AddDate(0, 0, 5)
	pre := reopened.Level()
	if err := reopened.ApplyQuote(Quote{Symbol: "C", At: day5, Price: 40}); err != nil {
		t.Fatalf("quote C: %v", err)
	}
	post := reopened.Level()
	if math.Abs(post.Price-pre.Price) > 1e-9 {
		t.Fatalf("recovered future change caused jump: %v vs %v", pre.Price, post.Price)
	}
}

func symbolName(index int) string {
	return "S" + string(rune('a'+index))
}

func changeName(day int) string {
	return "chg-future-" + time.Date(2026, 3, 1+day, 0, 0, 0, 0, time.UTC).Format("0102")
}
