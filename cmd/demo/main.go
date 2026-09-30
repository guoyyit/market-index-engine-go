package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	index "github.com/guoyyit/market-index-engine-go"
)

func main() {
	dir := filepath.Join(os.TempDir(), "market-index-engine-demo")
	if err := os.RemoveAll(dir); err != nil {
		fmt.Println("cleanup:", err)
		os.Exit(1)
	}
	engine, err := index.OpenEngine(dir)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}

	for _, symbol := range []string{"AAA", "BBB", "CCC"} {
		if err := engine.RegisterInstrument(index.Instrument{Symbol: symbol}); err != nil {
			fmt.Println("register:", err)
			os.Exit(1)
		}
	}
	base := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return base.AddDate(0, 0, n) }

	if err := engine.Initialize(
		[]index.Weight{
			{Symbol: "AAA", Weight: 0.6},
			{Symbol: "BBB", Weight: 0.4},
		},
		[]index.Quote{
			{Symbol: "AAA", At: base, Price: 10},
			{Symbol: "BBB", At: base, Price: 20},
		}); err != nil {
		fmt.Println("init:", err)
		os.Exit(1)
	}
	printLevel("基期", engine)

	if err := engine.RecordAction(index.CorporateAction{
		ID: "div-aaa", Symbol: "AAA", Kind: index.CashDividend,
		AnnouncedAt: day(1), ExDate: day(3), Dividend: 1,
	}); err != nil {
		fmt.Println("record dividend:", err)
		os.Exit(1)
	}

	if err := engine.SubmitChange(index.ConstituentChange{
		ID:          "rebalance-1",
		Adds:        []index.Weight{{Symbol: "CCC", Weight: 1.0 / 3}},
		WeightPatch: map[string]float64{"AAA": 1.0 / 3, "BBB": 1.0 / 3},
		AnnouncedAt: day(2),
		EffectiveAt: day(5),
	}); err != nil {
		fmt.Println("submit change:", err)
		os.Exit(1)
	}

	quote(engine, day(1), "AAA", 11)
	quote(engine, day(1), "BBB", 22)
	printLevel("第1天 上涨后", engine)

	quote(engine, day(3), "AAA", 10)
	quote(engine, day(3), "BBB", 22)
	printLevel("第3天 AAA 除息", engine)

	pre := engine.Level()
	quote(engine, day(5), "AAA", 10)
	quote(engine, day(5), "BBB", 22)
	quote(engine, day(5), "CCC", 30)
	post := engine.Level()
	printLevel("第5天 调样生效", engine)
	if abs(post.Price-pre.Price) > 1e-9 || abs(post.TotalReturn-pre.TotalReturn) > 1e-9 {
		fmt.Println("调样造成跳变！")
		os.Exit(1)
	}

	if err := engine.Checkpoint(); err != nil {
		fmt.Println("checkpoint:", err)
		os.Exit(1)
	}
	if err := engine.Close(); err != nil {
		fmt.Println("close:", err)
		os.Exit(1)
	}
	reopened, err := index.OpenEngine(dir)
	if err != nil {
		fmt.Println("reopen:", err)
		os.Exit(1)
	}
	defer reopened.Close()

	rebuilt, err := reopened.RebuildAt(day(3))
	if err != nil {
		fmt.Println("rebuild:", err)
		os.Exit(1)
	}
	fmt.Printf("\n重启后历史重建 @ %s：价格指数 %.4f，全收益 %.4f（再投资系数 %.6f）\n",
		rebuilt.At.Format("2006-01-02"), rebuilt.Price, rebuilt.TotalReturn,
		reopened.ReinvestmentFactor())
	printLevel("重启恢复的最新值", reopened)
}

func quote(engine *index.Engine, at time.Time, symbol string, price float64) {
	if err := engine.ApplyQuote(index.Quote{Symbol: symbol, At: at, Price: price}); err != nil {
		fmt.Printf("quote %s: %v\n", symbol, err)
		os.Exit(1)
	}
}

func printLevel(label string, engine *index.Engine) {
	level := engine.Level()
	fmt.Printf("%-18s 价格指数 %10.4f   全收益 %10.4f   除数(价/全) %.6f / %.6f\n",
		label, level.Price, level.TotalReturn, level.PriceDivisor, level.TRDivisor)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
