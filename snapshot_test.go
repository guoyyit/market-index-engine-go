package index

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRecoveryMatchesLogReplay(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")

	e, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	registerDemoIndex(t, e, 0)

	// Force more than snapshotEvery ticks so a snapshot is written.
	for n := 2; n < snapshotEvery+10; n++ {
		if err := e.OnTick("IDX", day(2).AddDate(0, 0, n-2), map[string]float64{
			"AAA": 10 + float64(n)*0.01,
			"BBB": 20 - float64(n)*0.005,
		}); err != nil {
			t.Fatal(err)
		}
	}
	liveP, _ := e.PriceIndex("IDX")
	liveTR, _ := e.TotalReturnIndex("IDX")
	if _, err := os.Stat(filepath.Join(storeDir, snapshotName)); err != nil {
		t.Fatalf("expected snapshot file: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := NewEngine(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	gotP, _ := e2.PriceIndex("IDX")
	gotTR, _ := e2.TotalReturnIndex("IDX")
	if math.Abs(gotP-liveP) > 1e-12 {
		t.Fatalf("post-snapshot price %.12f != live %.12f", gotP, liveP)
	}
	if math.Abs(gotTR-liveTR) > 1e-12 {
		t.Fatalf("post-snapshot TR %.12f != live %.12f", gotTR, liveTR)
	}

	// A full rebuild (log only, no snapshot) still agrees.
	points, err := e2.Rebuild("IDX")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != snapshotEvery+8 {
		t.Fatalf("rebuilt %d points", len(points))
	}
	if math.Abs(points[len(points)-1].Price-liveP) > 1e-12 {
		t.Fatalf("rebuild price mismatch: %.12f vs %.12f",
			points[len(points)-1].Price, liveP)
	}
}
