package index

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWritersAndReaders(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")
	e, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	registerDemoIndex(t, e, 0)

	var wg sync.WaitGroup

	// Many readers; they must never observe a partial/half-applied tick.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				cons, err := e.CurrentConstituents("IDX")
				if err != nil {
					t.Error(err)
					return
				}
				if len(cons) != 2 {
					t.Errorf("read inconsistent basket: %+v", cons)
					return
				}
				if _, err := e.PriceIndex("IDX"); err != nil {
					t.Error(err)
					return
				}
				if _, err := e.TotalReturnIndex("IDX"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}

	// Single tick writer advancing prices.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			if err := e.OnTick("IDX", day(2).Add(minutes(j)),
				map[string]float64{"AAA": 10 + float64(j)*0.01, "BBB": 20}); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	wg.Wait()

	points, err := e.Rebuild("IDX")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 100 {
		t.Fatalf("expected 100 replayed ticks, got %d", len(points))
	}
}

func minutes(n int) (d time.Duration) {
	return time.Duration(n) * time.Minute
}
