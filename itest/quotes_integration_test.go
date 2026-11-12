//go:build integration

package itest

import (
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/ids"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// quotes: quote requests and their fact, only while open (OP05); erp/sales' quotations, orders and refusals coming
// back by events: linked by version, a request answered or refused, and with the setting order_confirmed a confirmed
// order wins its opportunity by its net total, or by the expected amount in another currency; a won or lost
// opportunity is only linked; nothing older or unknown changes anything (OP08).
func (fx *fixture) quotes() {
	t, alice := fx.t, fx.alice
	newOpp := func(name, amount string) map[string]any {
		code, o := alice.do("POST", oppPath, map[string]any{"legal_entity_id": fx.le, "customer_id": fx.customer, "name": name,
			"amount": amount, "lines": []any{map[string]any{"product_id": ids.New().String(), "quantity": "2", "unit": "EA"}}})
		require.Equal(t, 200, code, o)
		return o
	}
	b := newOpp("报价的商机", "5000")
	id := b["id"].(string)

	// OP05: a request with the opportunity's lines; the same key answers the same request; lines given replace them
	key := ids.New().String()
	code, r1 := alice.do("POST", oppPath+"/"+id+"/quote-requests", nil, key)
	require.Equal(t, 200, code, r1)
	assert.Equal(t, "requested", r1["state"])
	assert.Equal(t, at(b, "lines", 0, "product_id"), at(r1, "lines", 0, "product_id"))
	fact := fx.outboxed(core.QuoteRequested, "fact_id", r1["id"].(string))
	require.NotNil(t, fact)
	assert.Equal(t, id, fact["opportunity_id"])
	assert.Equal(t, b["number"], fact["opportunity_number"])
	assert.Equal(t, "CNY", fact["currency"])
	code, again := alice.do("POST", oppPath+"/"+id+"/quote-requests", nil, key)
	require.Equal(t, 200, code, again)
	assert.Equal(t, r1["id"], again["id"])
	product := ids.New().String()
	code, r2 := alice.do("POST", oppPath+"/"+id+"/quote-requests", map[string]any{"lines": []any{
		map[string]any{"product_id": product, "quantity": "5", "unit": "BOX"}}})
	require.Equal(t, 200, code, r2)
	assert.Equal(t, product, at(fx.get(oppPath+"/"+id), "lines", 0, "product_id"), "the lines given replace the opportunity's")
	code, out := alice.do("POST", oppPath+"/"+fx.opps["A"]["id"].(string)+"/quote-requests", nil)
	assert.Equal(t, "STATE_CONFLICT", out["reason"], "a won opportunity asks for no quotation (OP05): %d", code)

	// OP08: erp/sales answers the first request with a draft and refuses the second
	quotation := ids.New().String()
	doc := func(docID, number, state, currency, net string, opp, request any) map[string]any {
		return map[string]any{"id": docID, "number": number, "state": state, "currency": currency, "opportunity_ref": opp,
			"quote_request_id": request, "totals": map[string]any{"net": net, "tax": "0", "gross": net}}
	}
	fx.publishSales(core.QuotationChanged, quotation, 1, doc(quotation, "QT-SH-1", "draft", "CNY", "0", id, r1["id"]))
	fx.publishSales(core.QuoteRequestRejected, r2["id"].(string), 1, map[string]any{"fact_id": r2["id"], "opportunity_id": id,
		"reason": "NOT_ELIGIBLE", "detail": "the customer is not eligible: partner_suspended"})
	var read map[string]any
	require.Eventually(t, func() bool {
		read = fx.get(oppPath + "/" + id)
		return at(read, "quote_requests", 0, "state") == "drafted" && at(read, "quote_requests", 1, "state") == "rejected"
	}, 10*time.Second, 100*time.Millisecond, "the draft and the refusal come back")
	assert.Equal(t, quotation, at(read, "quote_requests", 0, "quotation_id"))
	assert.Contains(t, at(read, "quote_requests", 1, "reason"), "NOT_ELIGIBLE")
	assert.Equal(t, "QT-SH-1", at(read, "sales_documents", 0, "number"))

	// the quotation accepted and its order a draft: linked, not won (the setting is order_confirmed)
	order := ids.New().String()
	fx.publishSales(core.QuotationChanged, quotation, 3, doc(quotation, "QT-SH-1", "accepted", "CNY", "4500", id, r1["id"]))
	fx.publishSales(core.OrderChanged, order, 1, doc(order, "SO-SH-1", "draft", "CNY", "4500", id, nil))
	require.Eventually(t, func() bool {
		read = fx.get(oppPath + "/" + id)
		return len(read["sales_documents"].([]any)) == 2 && at(read, "sales_documents", 0, "state") == "accepted"
	}, 10*time.Second, 100*time.Millisecond)
	assert.Equal(t, "qualification", read["stage"])

	// the order confirmed: won by its net total, by no user
	fx.publishSales(core.OrderChanged, order, 4, doc(order, "SO-SH-1", "confirmed", "CNY", "4500", id, nil))
	require.Eventually(t, func() bool { read = fx.get(oppPath + "/" + id); return read["stage"] == "won" },
		10*time.Second, 100*time.Millisecond, "a confirmed order wins its opportunity")
	assert.Equal(t, "4500", read["won_amount"])
	last := at(read, "stage_history", len(read["stage_history"].([]any))-1).(map[string]any)
	assert.Equal(t, "system", last["actor"])
	assert.Equal(t, "sales_order", at(last, "trigger", "kind"))
	assert.Equal(t, order, at(last, "trigger", "document_id"))
	won := fx.outboxed(core.Won, "opportunity_id", id)
	require.NotNil(t, won)
	assert.Equal(t, "sales_order", at(won, "trigger", "kind"))

	// an older version of the order changes nothing (checked below, after the later events had their turn)
	fx.publishSales(core.OrderChanged, order, 2, doc(order, "SO-SH-1", "draft", "CNY", "4500", id, nil))

	// in another currency: won by the expected amount, the stage change saying why
	c := newOpp("美元订单的商机", "7000")
	usdOrder := ids.New().String()
	fx.publishSales(core.OrderChanged, usdOrder, 2, doc(usdOrder, "SO-SH-2", "confirmed", "USD", "1000", c["id"], nil))
	require.Eventually(t, func() bool { read = fx.get(oppPath + "/" + c["id"].(string)); return read["stage"] == "won" },
		10*time.Second, 100*time.Millisecond)
	assert.Equal(t, "7000", read["won_amount"])
	last = at(read, "stage_history", len(read["stage_history"].([]any))-1).(map[string]any)
	assert.Contains(t, last["reason"], "USD")

	// a lost opportunity is only linked; a document by hand and one naming an unknown opportunity are dropped
	l := newOpp("输掉的商机", "100")
	code, l = alice.do("POST", oppPath+"/"+l["id"].(string)+"/lose", map[string]any{"version": l["version"], "reason": "预算砍了"})
	require.Equal(t, 200, code, l)
	late := ids.New().String()
	fx.publishSales(core.OrderChanged, late, 2, doc(late, "SO-SH-3", "confirmed", "CNY", "100", l["id"], nil))
	byHand, stray := ids.New().String(), ids.New().String()
	fx.publishSales(core.QuotationChanged, byHand, 1, doc(byHand, "QT-SH-9", "draft", "CNY", "0", nil, nil))
	fx.publishSales(core.OrderChanged, stray, 2, doc(stray, "SO-SH-9", "confirmed", "CNY", "1", ids.New().String(), nil))
	require.Eventually(t, func() bool {
		read = fx.get(oppPath + "/" + l["id"].(string))
		return len(read["sales_documents"].([]any)) == 1
	}, 10*time.Second, 100*time.Millisecond)
	assert.Equal(t, "lost", read["stage"], "a lost opportunity is not won by an order: a person reopens it")
	time.Sleep(300 * time.Millisecond) // the events published after the lost one's had their turn
	read = fx.get(oppPath + "/" + id)
	assert.Equal(t, "confirmed", at(read, "sales_documents", 1, "state"), "the older version did not overwrite")
	// the same confirmed order again: nothing more is won
	fx.publishSales(core.OrderChanged, order, 4, doc(order, "SO-SH-1", "confirmed", "CNY", "4500", id, nil))
	time.Sleep(300 * time.Millisecond)
	var wins, links int
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_outbox WHERE type = $1 AND "+
		"convert_from(payload, 'UTF8')::jsonb->>'opportunity_id' = $2", core.Won, id).Scan(&wins))
	assert.Equal(t, 1, wins, "the same confirmed order again won nothing more")
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".sales_link WHERE document_id IN ($1, $2)",
		byHand, stray).Scan(&links))
	assert.Zero(t, links)
	fx.opps["B"] = read
}
