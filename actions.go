package index

import "errors"

// shareFactors 表示一次公司行为在除权除息日，对价格指数与全收益指数
// “持有股数”分别需要乘以的系数。股数按系数放大等价于除数反向修正，
// 作用都是消除非交易因素造成的点位跳变。
type shareFactors struct {
	price float64 // 价格指数股数系数
	tr    float64 // 全收益指数股数系数
}

var (
	// ErrBadActionPrice 除权除息参考价非法（缺失或非正）。
	ErrBadActionPrice = errors.New("corporate action: cum price must be positive")
	// ErrBadDividend 分红金额非法或不低于除息前价格。
	ErrBadDividend = errors.New("corporate action: dividend must satisfy 0 < dividend < price")
	// ErrBadSplit 送转比例非法。
	ErrBadSplit = errors.New("corporate action: split factor must be > 1")
	// ErrBadRights 配股比例或配股价非法。
	ErrBadRights = errors.New("corporate action: rights ratio and price must be positive")
)

// actionShareFactors 按既定规则计算一次公司行为的股数调整系数。
// cumPrice 为除权除息日前一交易日收盘价（含权价）。
//
// 现金分红：
//
//	价格指数“不予修正、任其自然回落”，系数为 1；
//	全收益指数把现金按除息参考价再投资，系数为 p/(p-d)，
//	使除息后点位不回落，差额恰为再投资收益。
//
// 送转股（拆股）：
//
//	股数按 splitFactor 增加、价格等比下降，两条口径系数相同，
//	点位天然连续，等价于传统除数修正。
//
// 配股：
//
//	除权参考价 TERP = (p + r×s)/(1+r)，两条口径股数均乘以 p/TERP，
//	使按 TERP 除权后的点位与除权前严格连续。
func actionShareFactors(action CorporateAction, cumPrice float64) (shareFactors, error) {
	if cumPrice <= 0 {
		return shareFactors{}, ErrBadActionPrice
	}
	switch action.Kind {
	case CashDividend:
		if action.Dividend <= 0 || action.Dividend >= cumPrice {
			return shareFactors{}, ErrBadDividend
		}
		return shareFactors{
			price: 1,
			tr:    cumPrice / (cumPrice - action.Dividend),
		}, nil
	case StockSplit:
		if action.SplitFactor <= 1 {
			return shareFactors{}, ErrBadSplit
		}
		return shareFactors{price: action.SplitFactor, tr: action.SplitFactor}, nil
	case RightsIssue:
		if action.RightsRatio <= 0 || action.RightsPrice <= 0 {
			return shareFactors{}, ErrBadRights
		}
		terp := (cumPrice + action.RightsRatio*action.RightsPrice) / (1 + action.RightsRatio)
		factor := cumPrice / terp
		return shareFactors{price: factor, tr: factor}, nil
	default:
		return shareFactors{}, errors.New("corporate action: unknown kind")
	}
}

// reinvestmentFactor 返回全收益相对价格口径的累计换算系数增量。
// 仅现金分红产生非 1 的增量（= p/(p-d)），送转与配股对两口径影响完全相同。
func (factors shareFactors) reinvestmentFactor() float64 {
	if factors.price == 0 {
		return 1
	}
	return factors.tr / factors.price
}
