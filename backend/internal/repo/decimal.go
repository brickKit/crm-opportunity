package repo

import (
	"fmt"
	"math/big"
	"regexp"
)

// 金额、数量在契约里是十进制字符串，这里按十进制精确计算（math/big.Rat），
// 不经过 float64：float64 只有约 16 位有效数字，常见金额就会差一分，大额会
// 超出 NUMERIC(18,2) 的范围。舍入与 PostgreSQL 的 round(numeric, 2) 相同：
// 四舍五入到分，.5 远离零——漏斗统计在 SQL 里求和时用同一条规则，两边对得上。

// decimalRe 只认普通十进制写法（可带符号、小数、指数）。big.Rat.SetString
// 还接受 "1/3" 这种分数写法，金额字段不该接受它。
var decimalRe = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)

func parseDecimal(s string) (*big.Rat, bool) {
	if !decimalRe.MatchString(s) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}

var hundred = big.NewInt(100)

// formatCents 把 r 四舍五入到分（.5 远离零），输出两位小数。
func formatCents(r *big.Rat) string {
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(hundred))
	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// |rem| * 2 >= den 时远离零进一位。
	if new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2)).Cmp(den) >= 0 {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	sign := ""
	if q.Sign() < 0 {
		sign = "-"
		q.Abs(q)
	}
	cents := new(big.Int)
	units, _ := new(big.Int).QuoRem(q, hundred, cents)
	return fmt.Sprintf("%s%s.%02d", sign, units.String(), cents.Int64())
}
