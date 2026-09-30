package index

import "fmt"

// DefaultBaseLevel 指数基期点位，基期市值归一为 1，故基期除数为 1/baseLevel。
const DefaultBaseLevel = 1000.0

// series 表示一条指数口径（价格或全收益）的计算状态：
// 每个成分的“指数持有股数”与一个统一除数。
// 指数点位 = Σ(shares_i × price_i) / divisor。
type series struct {
	shares  map[string]float64
	divisor float64
}

func newSeries(divisor float64) *series {
	return &series{shares: map[string]float64{}, divisor: divisor}
}

// marketValue 成分调整后的合计市值（未除除数）。
func (s *series) marketValue(prices map[string]float64) float64 {
	sum := 0.0
	for symbol, held := range s.shares {
		sum += held * prices[symbol]
	}
	return sum
}

// level 当前指数点位。
func (s *series) level(prices map[string]float64) float64 {
	if s.divisor == 0 {
		return 0
	}
	return s.marketValue(prices) / s.divisor
}

// initAtBase 基期初始化：按归一目标权重与基期价格设定持有股数，
// 使 Σshares×price = 1，除数 = 1/baseLevel，指数恰好收于 baseLevel。
func (s *series) initAtBase(normalizedWeights, prices map[string]float64, baseLevel float64) error {
	shares := make(map[string]float64, len(normalizedWeights))
	for symbol, weight := range normalizedWeights {
		price, ok := prices[symbol]
		if !ok || price <= 0 {
			return fmt.Errorf("index init: missing positive price for %s", symbol)
		}
		shares[symbol] = weight / price
	}
	s.shares = shares
	s.divisor = 1.0 / baseLevel
	return nil
}

// reweight 在成分/权重变更生效时原子切换到新的归一权重集合。
// 股数按 shares'_i = w'_i / price_i 重设，新除数按
// divisor' = divisor × newSum / oldSum 修正，
// 保证切换前后指数点位严格相等（newSum = 1 时 divisor' = divisor/oldSum）。
// prices 必须包含新成分集合中每只股票的价格。
func (s *series) reweight(normalizedWeights, prices map[string]float64) error {
	if len(s.shares) == 0 {
		return fmt.Errorf("reweight: series not initialized")
	}
	oldSum := s.marketValue(prices)
	if oldSum <= 0 {
		return fmt.Errorf("reweight: non-positive old market value")
	}
	newShares := make(map[string]float64, len(normalizedWeights))
	newSum := 0.0
	for symbol, weight := range normalizedWeights {
		price, ok := prices[symbol]
		if !ok || price <= 0 {
			return fmt.Errorf("reweight: missing positive price for %s", symbol)
		}
		held := weight / price
		newShares[symbol] = held
		newSum += held * price
	}
	s.divisor = s.divisor * newSum / oldSum
	s.shares = newShares
	return nil
}

// scaleShares 对单个成分的持有股数乘以系数（送转、配股、再投资统一入口）。
func (s *series) scaleShares(symbol string, factor float64) {
	if held, ok := s.shares[symbol]; ok {
		s.shares[symbol] = held * factor
	}
}

// continuousDivisor 返回使指数在市值从 oldSum 变为 newSum 时保持连续的新除数。
func continuousDivisor(oldDivisor, oldSum, newSum float64) float64 {
	return oldDivisor * newSum / oldSum
}
