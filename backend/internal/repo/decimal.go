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

// decimalRe 只认普通十进制写法（可带负号与小数，不认指数）。它读的是库里
// NUMERIC 转出来的文本；调用方传进来的金额、数量走下面更严的校验。
// big.Rat.SetString 还接受 "1/3"、"1e3" 这些写法，这里一律不认。
var decimalRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// 入参校验：金额是 NUMERIC(18,2)，数量是 NUMERIC(18,6)，都不能为负。
// 写成正则而不是 strconv.ParseFloat 或交给数据库：ParseFloat 认 "NaN"、"Inf"、
// "1e3"；PostgreSQL 的 NUMERIC 认 'NaN' 并原样落库（NaN 比任何数都大，CHECK
// 拦不住，漏斗合计整列变成 NaN），认科学计数法，超出的小数位悄悄四舍五入，
// 整数位超长时报的是数据库错误（调用方拿到 500 而不是 400）。
var (
	amountRe = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,2})?$`)
	qtyRe    = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,6})?$`)
)

// ValidateAmount 校验一个金额入参：非负，最多 16 位整数、2 位小数。
func ValidateAmount(field, s string) error {
	if !amountRe.MatchString(s) {
		return fmt.Errorf("%w: %s 必须是非负十进制数，最多两位小数：%q", ErrInvalidArgument, field, s)
	}
	return nil
}

// ValidateQty 校验数量入参：大于 0，最多 12 位整数、6 位小数。
func ValidateQty(s string) error {
	if !qtyRe.MatchString(s) {
		return fmt.Errorf("%w: qty 必须是正的十进制数，最多六位小数：%q", ErrInvalidArgument, s)
	}
	if r, _ := new(big.Rat).SetString(s); r.Sign() <= 0 {
		return fmt.Errorf("%w: qty 必须大于 0：%q", ErrInvalidArgument, s)
	}
	return nil
}

// ValidateProbability 校验手工覆盖的概率：nil 表示用阶段默认值；否则 0–100。
func ValidateProbability(field string, p *int) error {
	if p != nil && (*p < 0 || *p > 100) {
		return fmt.Errorf("%w: %s 必须在 0–100 之间：%d", ErrInvalidArgument, field, *p)
	}
	return nil
}

// ValidateItem 校验一行商机行的数量与报价。
func ValidateItem(qty, unitPrice string) error {
	if err := ValidateQty(qty); err != nil {
		return err
	}
	return ValidateAmount("quoted_unit_price", unitPrice)
}

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
