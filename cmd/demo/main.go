package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	idx "github.com/guoyyit/market-index-engine-go"
)

func main() {
	dir := filepath.Join(os.TempDir(), "market-index-engine-demo")
	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}

	engine, err := idx.NewEngine(dir)
	if err != nil {
		panic(err)
	}
	defer engine.Close()

	base := time.Date(2025, 1, 1, 15, 0, 0, 0, time.UTC)
	def := idx.IndexDef{
		Code:      "DEMO",
		Name:      "Demo Index",
		BaseDate:  base,
		BaseValue: 1000,
	}
	// 60% AAA + 40% BBB; base raw sum = 0.6*10 + 0.4*20 = 14.
	if err := engine.RegisterIndex(def,
		[]idx.Constituent{
			{Symbol: "AAA", Weight: 0.6},
			{Symbol: "BBB", Weight: 0.4},
		},
		map[string]float64{"AAA": 10, "BBB": 20},
	); err != nil {
		panic(err)
	}

	show := func(label string, at time.Time, prices map[string]float64) {
		if err := engine.OnTick("DEMO", at, prices); err != nil {
			panic(err)
		}
		p, _ := engine.PriceIndex("DEMO")
		tr, _ := engine.TotalReturnIndex("DEMO")
		fmt.Printf("%-28s price=%8.3f  totalReturn=%8.3f\n", label, p, tr)
	}

	d := func(n int) time.Time { return base.AddDate(0, 0, n) }

	show("base tick", d(1), map[string]float64{"AAA": 10, "BBB": 20})

	// Cash dividend on AAA: price index drops with the quote; TR reinvests.
	if err := engine.RecordCorporateAction("DEMO", idx.CorporateAction{
		ID: "DIV-1", Symbol: "AAA", Type: idx.ActionCashDividend,
		AnnounceDate: d(1), ExDate: d(2), CashPerShare: 1,
	}); err != nil {
		panic(err)
	}
	show("AAA ex-dividend (9 +D=1)", d(2), map[string]float64{"AAA": 9, "BBB": 20})

	// Announce a rebalance effective in a few days: basket stays old until then.
	if err := engine.AnnounceRebalance("DEMO", idx.Rebalance{
		ID: "RB-1", AnnounceDate: d(2), EffectiveDate: d(5),
		Constituents: []idx.Constituent{
			{Symbol: "AAA", Weight: 0.5},
			{Symbol: "CCC", Weight: 0.5},
		},
	}); err != nil {
		panic(err)
	}
	show("announced, still AAA/BBB", d(3), map[string]float64{"AAA": 9, "BBB": 21, "CCC": 30})

	// 2-for-1 split on AAA: divisor adjusts, level continuous at ref price 4.5.
	if err := engine.RecordCorporateAction("DEMO", idx.CorporateAction{
		ID: "SPL-1", Symbol: "AAA", Type: idx.ActionSplit,
		AnnounceDate: d(3), ExDate: d(4), SplitRatio: 2,
	}); err != nil {
		panic(err)
	}
	show("AAA 2:1 split (9 -> 4.5)", d(4), map[string]float64{"AAA": 4.5, "BBB": 21, "CCC": 30})

	// Effective day: atomic basket switch AAA/BBB -> AAA/CCC with no jump.
	show("rebalance effective", d(5), map[string]float64{"AAA": 4.5, "BBB": 21, "CCC": 30})

	points, err := engine.Rebuild("DEMO")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nrebuilt %d historical points; final live == rebuilt point-by-point\n", len(points))

	fmt.Println("\nstored at:", dir)
	fmt.Println("events.log + snapshot.json persist state there; reopen with index.NewEngine(dir)")
}
