package index

import (
	"testing"
	"time"
)

func TestStoreAppendAndReopen(t *testing.T) {
	dir := t.TempDir()

	store, events, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("new store should have no events, got %d", len(events))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	first, err := store.Append(Event{Type: eventQuote, At: now, Quote: &Quote{Symbol: "A", Price: 10}})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if first.Seq != 1 {
		t.Fatalf("first seq = %d, want 1", first.Seq)
	}
	if _, err := store.Append(Event{Type: eventQuote, At: now, Quote: &Quote{Symbol: "B", Price: 20}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重新打开：序号连续、事件无丢失、无重复。
	reopened, reloaded, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if len(reloaded) != 2 {
		t.Fatalf("expected 2 events, got %d", len(reloaded))
	}
	if reloaded[0].Seq != 1 || reloaded[1].Seq != 2 {
		t.Fatalf("seqs after reopen: %d %d", reloaded[0].Seq, reloaded[1].Seq)
	}
	if reloaded[0].Quote.Symbol != "A" || reloaded[1].Quote.Price != 20 {
		t.Fatalf("event payload mismatch: %+v", reloaded)
	}
	appended, err := reopened.Append(Event{Type: eventQuote, At: now, Quote: &Quote{Symbol: "C"}})
	if err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if appended.Seq != 3 {
		t.Fatalf("seq must continue at 3, got %d", appended.Seq)
	}
}

func TestStoreSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	missing, err := LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("load missing snapshot: %v", err)
	}
	if missing != nil {
		t.Fatal("snapshot should be absent")
	}

	store, _, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	snapshot := Snapshot{
		Seq:          7,
		Cap:          0.2,
		Instruments:  map[string]Instrument{"A": {Symbol: "A", Name: "Alpha"}},
		PriceShares:  map[string]float64{"A": 0.05},
		TRShares:     map[string]float64{"A": 0.05},
		PriceDivisor: 0.001,
		TRDivisor:    0.001,
		Initialized:  true,
	}
	if err := store.SaveSnapshot(snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	loaded, err := LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if loaded == nil || loaded.Seq != 7 || loaded.Cap != 0.2 ||
		loaded.PriceDivisor != 0.001 ||
		loaded.Instruments["A"].Name != "Alpha" || !loaded.Initialized {
		t.Fatalf("snapshot round trip mismatch: %+v", loaded)
	}
}
