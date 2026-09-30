package index

import "time"

// IndexDef describes an index: identity, base period and optional weight cap.
type IndexDef struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	BaseDate  time.Time `json:"base_date"`
	BaseValue float64   `json:"base_value"`
	WeightCap float64   `json:"weight_cap,omitempty"` // e.g. 0.10 means 10%; 0 disables the cap
}

// Constituent is one index member with its target weight.
type Constituent struct {
	Symbol string  `json:"symbol"`
	Weight float64 `json:"weight"`
}

// ActionType enumerates the supported corporate actions.
type ActionType string

const (
	// ActionCashDividend is a cash dividend per share (ex-dividend date).
	ActionCashDividend ActionType = "cash_dividend"
	// ActionSplit is a bonus share issue / stock split (送转).
	ActionSplit ActionType = "split"
	// ActionRightsIssue is a rights offering (配股).
	ActionRightsIssue ActionType = "rights_issue"
)

// CorporateAction is a company action announced for one constituent.
type CorporateAction struct {
	ID           string     `json:"id"`
	Symbol       string     `json:"symbol"`
	Type         ActionType `json:"type"`
	AnnounceDate time.Time  `json:"announce_date"`
	ExDate       time.Time  `json:"ex_date"`
	// CashDividend: cash amount per old share.
	CashPerShare float64 `json:"cash_per_share,omitempty"`
	// Split: old holdings are multiplied by SplitRatio (10送5 -> 1.5).
	SplitRatio float64 `json:"split_ratio,omitempty"`
	// RightsIssue: number of rights shares offered per old share.
	RightsRatio float64 `json:"rights_ratio,omitempty"`
	// RightsIssue: subscription price per rights share.
	RightsPrice float64 `json:"rights_price,omitempty"`
	// Applied is set once the action has taken effect on an ex-date tick.
	Applied bool `json:"applied,omitempty"`
}

// Rebalance is an announced constituent/weight change with a future effective date.
type Rebalance struct {
	ID            string        `json:"id"`
	AnnounceDate  time.Time     `json:"announce_date"`
	EffectiveDate time.Time     `json:"effective_date"`
	Constituents  []Constituent `json:"constituents"`
}

// Tick is one set of observed prices at a point in time, supplied by the local caller.
type Tick struct {
	Time   time.Time          `json:"time"`
	Prices map[string]float64 `json:"prices"`
}

// EventType enumerates persisted event-log record types.
type EventType string

const (
	EvIndexRegistered    EventType = "index_registered"
	EvRebalanceAnnounced EventType = "rebalance_announced"
	EvActionRecorded     EventType = "action_recorded"
	EvTick               EventType = "tick"
)

// Adjustment records one divisor / total-return-factor change so it can be replayed
// identically during a historical rebuild.
type Adjustment struct {
	Reason      string  `json:"reason"`
	Symbol      string  `json:"symbol,omitempty"`
	OldSum      float64 `json:"old_sum"`
	NewSum      float64 `json:"new_sum"`
	OldDivisor  float64 `json:"old_divisor"`
	NewDivisor  float64 `json:"new_divisor"`
	OldTRFactor float64 `json:"old_tr_factor"`
	NewTRFactor float64 `json:"new_tr_factor"`
}

// Event is one immutable record in the append-only event log.
type Event struct {
	Seq         int64              `json:"seq"`
	Time        time.Time          `json:"time"`
	Type        EventType          `json:"type"`
	IndexCode   string             `json:"index_code,omitempty"`
	Def         *IndexDef          `json:"def,omitempty"`
	Base        []Constituent      `json:"base,omitempty"`
	BasePrices  map[string]float64 `json:"base_prices,omitempty"`
	Rebalance   *Rebalance         `json:"rebalance,omitempty"`
	Action      *CorporateAction   `json:"action,omitempty"`
	Tick        *Tick              `json:"tick,omitempty"`
	Adjustments []Adjustment       `json:"adjustments,omitempty"`
}

// indexState is the mutable state of a single index.
type indexState struct {
	Def       IndexDef          `json:"def"`
	Current   []Constituent     `json:"current"`
	Divisor   float64           `json:"divisor"`
	TRFactor  float64           `json:"tr_factor"`
	Scheduled []Rebalance       `json:"scheduled"`
	Actions   []CorporateAction `json:"actions"`
	Ticks     []Tick            `json:"ticks"`
	LastTime  time.Time         `json:"last_time"`
}

// engineState is the full snapshotable state of the engine.
type engineState struct {
	Seq     int64                  `json:"seq"`
	Indices map[string]*indexState `json:"indices"`
}

// LevelPoint is one reconstructed index observation.
type LevelPoint struct {
	Time        time.Time `json:"time"`
	Price       float64   `json:"price"`
	TotalReturn float64   `json:"total_return"`
}
