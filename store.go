package index

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// 事件类型。事件日志是系统的唯一事实来源（source of truth），
// 快照仅用于加速重启重放，任何情况下都不得改变重建结果。
const (
	eventInstrument   = "instrument"    // 登记证券
	eventBase         = "base"          // 基期初始化
	eventQuote        = "quote"         // 一条本地行情
	eventReweight     = "reweight"      // 生效的成分/权重调样
	eventAction       = "action"        // 除权除息日应用的公司行为
	eventChangeSubmit = "change_submit" // 公告日登记的调样单（尚未生效）
	eventActionSubmit = "action_submit" // 公告日登记的公司行为（尚未除息）
)

// Event 是持久化事件的统一信封。时间戳同时作为事件顺序的裁决依据。
type Event struct {
	Seq        int64              `json:"seq"`
	Type       string             `json:"type"`
	At         string             `json:"at"` // RFC3339Nano，保证时序可重放
	Instrument *Instrument        `json:"instrument,omitempty"`
	Weights    []Weight           `json:"weights,omitempty"` // base/reweight 生效的封顶后权重
	Quotes     []Quote            `json:"quotes,omitempty"`  // base 初始化的基期价格集合
	Quote      *Quote             `json:"quote,omitempty"`
	Action     *CorporateAction   `json:"action,omitempty"`
	Change     *ConstituentChange `json:"change,omitempty"`
	ChangeID   string             `json:"change_id,omitempty"` // reweight 对应调样单
	Cap        float64            `json:"cap,omitempty"`       // base 时的封顶上限
}

// Snapshot 为状态快照：与事件日志前缀配合即可完整恢复。
type Snapshot struct {
	Seq            int64                        `json:"seq"`
	Cap            float64                      `json:"cap"`
	Instruments    map[string]Instrument        `json:"instruments"`
	PriceShares    map[string]float64           `json:"price_shares"`
	TRShares       map[string]float64           `json:"tr_shares"`
	PriceDivisor   float64                      `json:"price_divisor"`
	TRDivisor      float64                      `json:"tr_divisor"`
	Initialized    bool                         `json:"initialized"`
	PendingChanges map[string]ConstituentChange `json:"pending_changes,omitempty"`
	PendingActions map[string]CorporateAction   `json:"pending_actions,omitempty"`
	AppliedChanges map[string]bool              `json:"applied_changes,omitempty"`
	AppliedActions map[string]bool              `json:"applied_actions,omitempty"`
	Prices         map[string]float64           `json:"prices,omitempty"`
	LastQuoteAt    string                       `json:"last_quote_at,omitempty"`
}

var (
	// ErrEventExists 表示同一业务 ID（调样单/公司行为）已经应用过，拒绝重复。
	ErrEventExists = errors.New("event with same business id already applied")
)

const (
	logFileName  = "events.log"
	snapshotName = "snapshot.json"
	snapshotTemp = "snapshot.json.tmp"
)

// Store 管理本地事件日志与快照。追加采用 fsync 落盘；
// 快照以“写临时文件 + 原子重命名”方式替换，重启不会读到半个快照。
type Store struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	seq  int64
}

// OpenStore 打开（必要时创建）目录下的事件日志，返回当前已持久化事件。
func OpenStore(dir string) (*Store, []Event, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("open store: %w", err)
	}
	path := filepath.Join(dir, logFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open store: %w", err)
	}
	store := &Store{dir: dir, file: file}
	events, err := store.readAll()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return store, events, nil
}

// Append 将事件序列化、追加、fsync 后返回带序号的事件。
func (store *Store) Append(event Event) (Event, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.seq++
	event.Seq = store.seq
	payload, err := json.Marshal(event)
	if err != nil {
		store.seq--
		return Event{}, fmt.Errorf("append event: %w", err)
	}
	if _, err := store.file.Write(append(payload, '\n')); err != nil {
		store.seq--
		return Event{}, fmt.Errorf("append event: %w", err)
	}
	if err := store.file.Sync(); err != nil {
		return Event{}, fmt.Errorf("sync event: %w", err)
	}
	return event, nil
}

// Seq 返回已持久化的事件总数。
func (store *Store) Seq() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.seq
}

// Close 关闭日志文件。
func (store *Store) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.file == nil {
		return nil
	}
	err := store.file.Close()
	store.file = nil
	return err
}

func (store *Store) readAll() ([]Event, error) {
	raw, err := os.Open(filepath.Join(store.dir, logFileName))
	if err != nil {
		return nil, fmt.Errorf("read log: %w", err)
	}
	defer raw.Close()
	var events []Event
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("corrupt event log: %w", err)
		}
		events = append(events, event)
		if event.Seq > store.seq {
			store.seq = event.Seq
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read log: %w", err)
	}
	return events, nil
}

// SaveSnapshot 原子写入快照（临时文件 + fsync + rename）。
func (store *Store) SaveSnapshot(snapshot Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	tmp := filepath.Join(store.dir, snapshotTemp)
	final := filepath.Join(store.dir, snapshotName)
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

// LoadSnapshot 读取快照；不存在时返回 (nil, nil)。
func LoadSnapshot(dir string) (*Snapshot, error) {
	file, err := os.Open(filepath.Join(dir, snapshotName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	defer file.Close()
	var snapshot Snapshot
	if err := json.NewDecoder(file).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return &snapshot, nil
}
