//go:build integration

package itest

import (
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// funnel: Beijing's opportunities by currency and stage, weighted by the stage's probability and rounded to the
// currency's minor units; won counts its won amount, lost weighs 0; a salesperson's funnel counts only what he sees;
// the owner and the expected close dates filter.
func (fx *fixture) funnel() {
	t, alice := fx.t, fx.alice
	create := func(c client, name, amount, stage, close string, probability any) map[string]any {
		in := map[string]any{"legal_entity_id": fx.le2, "customer_id": fx.customer, "name": name, "amount": amount,
			"stage": stage, "expected_close_date": close}
		if probability != nil {
			in["probability"] = probability
		}
		code, o := c.do("POST", oppPath, in)
		require.Equal(t, 200, code, o)
		return o
	}
	create(alice, "北京一", "1000", "qualification", "2026-12-01", nil)  // weighs 100
	create(alice, "北京二", "333.33", "qualification", "2027-03-01", 33) // weighs 109.9989, rounded 110.00
	create(fx.dave, "北京 Dave", "800", "proposal", "2026-12-15", nil)  // weighs 400
	won := create(alice, "北京赢", "2000", "negotiation", "2026-11-30", nil)
	code, won := alice.do("POST", oppPath+"/"+won["id"].(string)+"/win", map[string]any{"version": won["version"], "won_amount": "1900"})
	require.Equal(t, 200, code, won)
	lost := create(alice, "北京输", "500", "proposal", "2026-11-30", nil)
	code, lost = alice.do("POST", oppPath+"/"+lost["id"].(string)+"/lose", map[string]any{"version": lost["version"], "reason": "价格"})
	require.Equal(t, 200, code, lost)

	rowsOf := func(c client, query string) map[string]map[string]any {
		code, out := c.do("GET", "/api/opportunity/funnel?legal_entity_id="+fx.le2+query, nil)
		require.Equal(t, 200, code, out)
		got := map[string]map[string]any{}
		for _, r := range out["rows"].([]any) {
			row := r.(map[string]any)
			got[row["stage"].(string)] = row
		}
		return got
	}
	all := rowsOf(alice, "")
	require.Len(t, all, 4, "%v", all)
	assert.EqualValues(t, 2, all["qualification"]["count"])
	assert.Equal(t, "1333.33", all["qualification"]["amount"])
	assert.Equal(t, "210", all["qualification"]["weighted"], "100 + 109.9989 rounded to 2 places is 209.9989 → 210.00")
	assert.Equal(t, "400", all["proposal"]["weighted"])
	assert.Equal(t, "1900", all["won"]["amount"], "won counts its won amount")
	assert.Equal(t, "1900", all["won"]["weighted"])
	assert.Equal(t, "0", all["lost"]["weighted"])
	assert.Equal(t, "CNY", all["won"]["currency"])

	mine := rowsOf(fx.dave, "")
	require.Len(t, mine, 1, "a salesperson's funnel counts only his: %v", mine)
	assert.Equal(t, "800", mine["proposal"]["amount"])
	assert.Len(t, rowsOf(alice, "&owner_id="+dave), 1)
	soon := rowsOf(alice, "&close_to=2026-12-31")
	assert.EqualValues(t, 1, soon["qualification"]["count"], "北京二 closes in 2027")
	for _, q := range []string{"legal_entity_id=not-a-uuid", "owner_id=someone", "close_to=31.12.2026"} {
		code, _ = alice.do("GET", "/api/opportunity/funnel?"+q, nil)
		assert.Equal(t, 400, code, q)
	}
}
