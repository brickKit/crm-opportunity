//go:build integration

package itest

import (
	"regexp"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

const oppPath = "/api/opportunity/opportunities"

// opportunities: creating with the evidence of the legal entity, the currency and the customer (OP01); changing by
// version and the won lock (OP02); stages forward past others and back with a reason (OP03); the owner by another
// operation (OP04); win, lose and reopen (OP06, OP07), all by hand: erp/sales is not in this test's assembly, and
// winning works without it.
func (fx *fixture) opportunities() {
	t, alice, bob, rep := fx.t, fx.alice, fx.bob, fx.dave

	// OP01: create; the currency is the legal entity's, the owner the caller, the stage qualification at 10
	key := ids.New().String()
	body := map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "上海客户的螺丝", "amount": "10000",
		"expected_close_date": "2026-12-31", "lines": []any{map[string]any{"product_id": ids.New().String(), "quantity": "3", "unit": "EA"}}}
	code, a := alice.do("POST", oppPath, body, key)
	require.Equal(t, 200, code, a)
	assert.Regexp(t, regexp.MustCompile(`^OP-SH-\d{4}-\d{6}$`), a["number"])
	assert.Equal(t, "CNY", a["currency"])
	assert.Equal(t, sectest.Alice, a["owner_id"])
	assert.Equal(t, "qualification", a["stage"])
	assert.EqualValues(t, 10, a["probability"])
	assert.Equal(t, "上海客户", at(a, "customer", "name"))
	assert.Equal(t, "3", at(a, "lines", 0, "quantity"))
	assert.Nil(t, at(a, "stage_history", 0, "from"))
	assert.Equal(t, "qualification", at(a, "stage_history", 0, "to"))
	assert.NotNil(t, fx.outboxed(core.StageChanged, "opportunity_id", a["id"].(string)))
	code, again := alice.do("POST", oppPath, body, key)
	require.Equal(t, 200, code, again)
	assert.Equal(t, a["id"], again["id"], "the same Idempotency-Key answers the same opportunity")

	for _, c := range []struct {
		change map[string]any
		reason string
	}{
		{map[string]any{"legal_entity_id": fx.le2, "customer_id": fx.shOnly}, "CUSTOMER_NOT_ELIGIBLE"}, // no customer role in Beijing
		{map[string]any{"customer_id": fx.blocked}, "CUSTOMER_NOT_ELIGIBLE"},
		{map[string]any{"legal_entity_id": ids.New().String()}, "REFERENCE_INVALID"},
		{map[string]any{"currency": "EUR"}, "REFERENCE_INVALID"},
		{map[string]any{"amount": "1.234"}, "REQUEST_INVALID"},
		{map[string]any{"stage": "won"}, "REQUEST_INVALID"},
		{map[string]any{"name": ""}, "REQUEST_INVALID"},
	} {
		in := map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "被拒的商机"}
		for k, v := range c.change {
			in[k] = v
		}
		code, out := alice.do("POST", oppPath, in)
		assert.Equal(t, c.reason, out["reason"], "%v: %d %v", c.change, code, out)
	}
	code, eligible := alice.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.shOnly,
		"name": "只在上海的客户在上海", "currency": "USD", "amount": "500.5", "stage": "proposal"})
	require.Equal(t, 200, code, eligible)
	assert.EqualValues(t, 50, eligible["probability"], "a stage's default probability")

	// OP04: a salesperson creates his own; another owner needs the assign operation
	code, d := rep.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "Dave 的商机",
		"amount": "300"})
	require.Equal(t, 200, code, d)
	assert.Equal(t, dave, d["owner_id"])
	code, _ = rep.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "给 Alice 的",
		"owner_id": sectest.Alice})
	assert.Equal(t, 404, code, "an owner the caller may not see answers 404")
	code, out := fx.erin.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "Erin 替 Alice 建",
		"owner_id": sectest.Alice})
	assert.Equal(t, 403, code, "an owner other than the caller needs crm.opportunity.assign: %v", out)
	code, out = fx.bob.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": "Bob 替 Alice 建",
		"owner_id": sectest.Alice})
	require.Equal(t, 200, code, out)
	assert.Equal(t, sectest.Alice, out["owner_id"])

	// OP02: change by version
	id := a["id"].(string)
	code, wrong := alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": 99, "amount": "1"})
	assert.Equal(t, "VERSION_CONFLICT", wrong["reason"], code)
	code, a = alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": a["version"], "amount": "12000", "name": "螺丝与螺母"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "12000", a["amount"])

	// OP03: forward past a stage; not to the same or an earlier one; back with a reason; a probability given
	act := func(c client, verb string, in map[string]any) (int, map[string]any) {
		in["version"] = a["version"]
		return c.do("POST", oppPath+"/"+id+"/"+verb, in)
	}
	code, a = act(alice, "advance", map[string]any{"stage": "proposal"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "proposal", a["stage"])
	assert.EqualValues(t, 50, a["probability"])
	for _, s := range []string{"proposal", "qualification"} {
		code, out := act(alice, "advance", map[string]any{"stage": s})
		assert.Equal(t, "STAGE_ORDER", out["reason"], "advance to %s: %d", s, code)
	}
	code, out = act(alice, "advance", map[string]any{"stage": "won"})
	assert.Equal(t, "STAGE_ORDER", out["reason"], "won is reached by winning: %d", code)
	code, out = act(alice, "revert", map[string]any{"stage": "needs_analysis"})
	assert.Equal(t, "REQUEST_INVALID", out["reason"], "a revert needs its reason: %d", code)
	code, out = act(alice, "revert", map[string]any{"stage": "negotiation", "reason": "向前不是回退"})
	assert.Equal(t, "STAGE_ORDER", out["reason"], code)
	code, a = act(alice, "revert", map[string]any{"stage": "needs_analysis", "reason": "客户换了联系人"})
	require.Equal(t, 200, code, a)
	assert.EqualValues(t, 25, a["probability"])
	code, a = act(alice, "advance", map[string]any{"stage": "negotiation", "probability": 80})
	require.Equal(t, 200, code, a)
	assert.EqualValues(t, 80, a["probability"])
	assert.Len(t, a["stage_history"], 4)
	assert.Equal(t, "客户换了联系人", at(a, "stage_history", 2, "reason"))

	// OP04: Dave sees only his own and does not assign; Bob, a manager, assigns Dave's to Alice
	code, page := rep.do("GET", oppPath+"?limit=50", nil)
	require.Equal(t, 200, code, page)
	require.Len(t, page["opportunities"], 1)
	assert.Equal(t, d["id"], at(page, "opportunities", 0, "id"))
	code, _ = rep.do("GET", oppPath+"/"+id, nil)
	assert.Equal(t, 404, code, "another's opportunity is not seen")
	code, _ = rep.do("POST", oppPath+"/"+d["id"].(string)+"/assign", map[string]any{"version": d["version"], "owner_id": sectest.Alice})
	assert.Equal(t, 403, code)
	code, d = bob.do("POST", oppPath+"/"+d["id"].(string)+"/assign", map[string]any{"version": d["version"], "owner_id": sectest.Alice})
	require.Equal(t, 200, code, d)
	assert.Equal(t, sectest.Alice, d["owner_id"])
	code, _ = rep.do("GET", oppPath+"/"+d["id"].(string), nil)
	assert.Equal(t, 404, code, "assigned away, no longer seen")

	// OP06: won with its amount; again is the same and publishes nothing more; the amount is locked
	code, a = act(alice, "win", map[string]any{"won_amount": "11800"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "won", a["stage"])
	assert.EqualValues(t, 100, a["probability"])
	assert.Equal(t, "11800", a["won_amount"])
	assert.Equal(t, "negotiation", a["stage_before_close"])
	code, same := act(alice, "win", map[string]any{})
	require.Equal(t, 200, code, same)
	assert.Equal(t, a["version"], same["version"], "winning a won opportunity changes nothing")
	var wins int
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_outbox WHERE type = $1 AND "+
		"convert_from(payload, 'UTF8')::jsonb->>'opportunity_id' = $2", core.Won, id).Scan(&wins))
	assert.Equal(t, 1, wins)
	assert.Equal(t, "11800", fx.outboxed(core.Won, "opportunity_id", id)["won_amount"])
	code, out = alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": a["version"], "amount": "20000"})
	assert.Equal(t, "WON_AMOUNT_LOCKED", out["reason"], code)
	code, a = alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": a["version"], "name": "螺丝与螺母（已签）"})
	require.Equal(t, 200, code, a)
	for _, verb := range []string{"advance", "lose"} {
		code, out := act(alice, verb, map[string]any{"stage": "negotiation", "reason": "试试"})
		assert.Equal(t, "STATE_CONFLICT", out["reason"], "%s on a won opportunity: %d", verb, code)
	}

	// OP07: reopen with a reason, back to the stage and probability before; then lost (OP06) and reopened again
	code, out = act(alice, "reopen", map[string]any{})
	assert.Equal(t, "REQUEST_INVALID", out["reason"], code)
	code, a = act(alice, "reopen", map[string]any{"reason": "客户要加量"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "negotiation", a["stage"])
	assert.EqualValues(t, 80, a["probability"])
	assert.Nil(t, a["won_amount"])
	code, a = act(alice, "reopen", map[string]any{"reason": "再开一次"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "negotiation", a["stage"], "reopening an open opportunity is ignored")
	code, a = alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": a["version"], "amount": "20000"})
	require.Equal(t, 200, code, a)
	code, out = act(alice, "lose", map[string]any{})
	assert.Equal(t, "REQUEST_INVALID", out["reason"], code)
	code, a = act(alice, "lose", map[string]any{"reason": "客户选了别家"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "lost", a["stage"])
	assert.EqualValues(t, 0, a["probability"])
	assert.Equal(t, "客户选了别家", fx.outboxed(core.Lost, "opportunity_id", id)["reason"])
	code, same = act(alice, "lose", map[string]any{"reason": "又一次"})
	require.Equal(t, 200, code, same)
	assert.Equal(t, a["version"], same["version"])
	code, out = act(alice, "win", map[string]any{})
	assert.Equal(t, "STATE_CONFLICT", out["reason"], "a lost opportunity is reopened before it is won: %d", code)
	code, out = alice.do("PATCH", oppPath+"/"+id, map[string]any{"version": a["version"], "name": "改"})
	assert.Equal(t, "STATE_CONFLICT", out["reason"], code)
	code, a = act(alice, "reopen", map[string]any{"reason": "客户回来了"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "negotiation", a["stage"])
	code, a = act(alice, "win", map[string]any{})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "20000", a["won_amount"], "the expected amount when none is given")
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_outbox WHERE type = $1 AND "+
		"convert_from(payload, 'UTF8')::jsonb->>'opportunity_id' = $2", core.Won, id).Scan(&wins))
	assert.Equal(t, 2, wins, "won again after reopening: a new fact")

	fx.opps["A"], fx.opps["D"], fx.opps["usd"] = a, d, eligible
}
