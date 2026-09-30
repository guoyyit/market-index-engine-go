// Package index 实现本地运行的证券市场指数计算与成分管理服务，
// 同时维护价格指数与全收益指数两种口径。
package index

import "time"

// IndexKind 指数口径。
type IndexKind int

const (
	// Price 价格指数：仅反映价格变动，分红后除数自然回落。
	Price IndexKind = iota
	// TotalReturn 全收益指数：分红按除息日收盘再投资计入。
	TotalReturn
)

// Instrument 证券基础信息，由本地调用方登记。
type Instrument struct {
	Symbol string
	Name   string
}

// Weight 成分及其目标权重（原始权重，封顶前）。
type Weight struct {
	Symbol string
	Weight float64
}

// ConstituentChange 一次成分/权重调样，带公告日与生效日。
// 生效日之前一律按旧成分计算，生效日原子切换。
type ConstituentChange struct {
	ID          string
	Adds        []Weight           // 新增成分或纳入的目标权重
	Removes     []string           // 删除的成分代码
	WeightPatch map[string]float64 // 保留成分的目标权重调整
	AnnouncedAt time.Time
	EffectiveAt time.Time
}

// ActionKind 公司行为类型。
type ActionKind int

const (
	// CashDividend 现金分红。
	CashDividend ActionKind = iota
	// StockSplit 送股/转增/拆股，按比例增加股数。
	StockSplit
	// RightsIssue 配股，按比例折价认购新股。
	RightsIssue
)

// CorporateAction 公司行为登记。
type CorporateAction struct {
	ID          string
	Symbol      string
	Kind        ActionKind
	AnnouncedAt time.Time
	ExDate      time.Time // 除权除息日
	Dividend    float64   // 现金分红：每股派现（税前）
	SplitFactor float64   // 送转：每股拆成多少股，10送10 => 2
	RightsRatio float64   // 配股：每股可认购新股数
	RightsPrice float64   // 配股：认购价格

	// CumPrice 为除权除息结算时快照的除息前收盘价。录入时可由调用方提供，
	// 若为 0 则在除息日结算时由系统按当时最新价填充；会随事件持久化以精确重建。
	CumPrice float64
}

// Quote 本地调用方提供的行情快照（未复权价格）。
type Quote struct {
	Symbol string
	At     time.Time
	Price  float64
}

// IndexValue 某一时点的双口径指数值与对应除数。
type IndexValue struct {
	At           time.Time
	Price        float64
	TotalReturn  float64
	PriceDivisor float64
	TRDivisor    float64
}
