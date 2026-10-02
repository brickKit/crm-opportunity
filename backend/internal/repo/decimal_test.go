package repo

import "testing"

// 金额在契约里一律是十进制字符串，算加权金额与行小计也必须按十进制算：
// 走 float64 时，常见金额就会差一分（100.05 × 50% 得 50.02），大额会差得更多，
// 甚至超出 NUMERIC(18,2) 让写库失败。舍入规则与 PostgreSQL 的 round(numeric)
// 相同：四舍五入到分，.5 远离零。

func TestComputeWeightedAmount_按十进制算并四舍五入到分(t *testing.T) {
	cases := []struct {
		amount      string
		probability int
		want        string
	}{
		{"10000.00", 10, "1000.00"},
		{"100.05", 50, "50.03"},                            // 50.025 → 50.03
		{"1234567890123456.78", 10, "123456789012345.68"},  // 123456789012345.678
		{"9999999999999999.99", 70, "6999999999999999.99"}, // 6999999999999999.993
		{"0", 30, "0.00"},
		{"1.15", 10, "0.12"}, // 0.115 → 0.12
	}
	for _, c := range cases {
		if got := computeWeightedAmount(c.amount, c.probability); got != c.want {
			t.Errorf("computeWeightedAmount(%q, %d) = %q, want %q", c.amount, c.probability, got, c.want)
		}
	}
}

func TestComputeSubtotal_按十进制算并四舍五入到分(t *testing.T) {
	cases := []struct{ qty, price, want string }{
		{"2", "500.00", "1000.00"},
		{"3", "33333333333333.33", "99999999999999.99"},
		{"1", "9999999999999999.99", "9999999999999999.99"},
		{"0.5", "0.05", "0.03"}, // 0.025 → 0.03
		{"1.5", "10", "15.00"},
	}
	for _, c := range cases {
		got, err := computeSubtotal(c.qty, c.price)
		if err != nil {
			t.Errorf("computeSubtotal(%q, %q) 报错：%v", c.qty, c.price, err)
			continue
		}
		if got != c.want {
			t.Errorf("computeSubtotal(%q, %q) = %q, want %q", c.qty, c.price, got, c.want)
		}
	}
}

func TestComputeSubtotal_非法数字报参数错误(t *testing.T) {
	for _, bad := range [][2]string{{"abc", "1"}, {"1", "1/3"}, {"1", "NaN"}, {"", "1"}} {
		if _, err := computeSubtotal(bad[0], bad[1]); err == nil {
			t.Errorf("computeSubtotal(%q, %q) 应该报参数错误", bad[0], bad[1])
		}
	}
}
