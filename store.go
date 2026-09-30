package index

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	logFileName     = "events.log"
	snapshotName    = "snapshot.json"
	snapshotTmpName = "snapshot.json.tmp"
)

// fileStore is an append-only JSON event log with periodic JSON snapshots.
// Appends are flushed before they are applied in memory, so an already-effective
// adjustment is never lost; snapshots are written atomically (temp + rename).
type fileStore struct {
	dir string
	f   *os.File
	w   *bufio.Writer
	enc *json.Encoder
}

func openStore(dir string) (*fileStore, error) {
	if dir == "" {
		return nil, errors.New("index: storage directory required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dir, logFileName)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriter(f)
	return &fileStore{
		dir: dir,
		f:   f,
		w:   w,
		enc: json.NewEncoder(w),
	}, nil
}

func (s *fileStore) append(ev Event) error {
	if err := s.enc.Encode(ev); err != nil {
		return err
	}
	// Flush before the caller applies the event: durability precedes visibility.
	if err := s.w.Flush(); err != nil {
		return err
	}
	return s.f.Sync()
}

func (s *fileStore) eventsAfter(seq int64) ([]Event, error) {
	f, err := os.Open(filepath.Join(s.dir, logFileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	dec := json.NewDecoder(bufio.NewReader(f))
	for dec.More() {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			return nil, err
		}
		if ev.Seq > seq {
			events = append(events, ev)
		}
	}
	return events, nil
}

func (s *fileStore) saveSnapshot(seq int64, state *engineState) error {
	state.Seq = seq
	tmp := filepath.Join(s.dir, snapshotTmpName)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(state); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Atomic publish.
	return os.Rename(tmp, filepath.Join(s.dir, snapshotName))
}

func (s *fileStore) loadSnapshot() (int64, *engineState, error) {
	f, err := os.Open(filepath.Join(s.dir, snapshotName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	defer f.Close()

	var state engineState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return 0, nil, err
	}
	return state.Seq, &state, nil
}

func (s *fileStore) close() error {
	return s.f.Close()
}
