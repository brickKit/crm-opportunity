package quotes

import (
	"context"
	"encoding/json"
	"errors"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	opportunities "github.com/brickKit/crm-opportunity/v3/internal/opportunities"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// AutoWins says whether a sales document in a state wins its opportunity under the deployment's setting (OP08):
// quotation_accepted wins on an accepted quotation, order_confirmed on a confirmed order, manual never. Nothing loses
// an opportunity by itself.
func AutoWins(setting, kind, state string) bool {
	switch setting {
	case core.AutoQuotation:
		return kind == KindQuotation && state == "accepted"
	case core.AutoOrder:
		return kind == KindOrder && state == "confirmed"
	}
	return false
}

// The kinds of sales documents (sales_link.kind).
const (
	KindQuotation = "quotation"
	KindOrder     = "sales_order"
)

// document is what OP08 reads of erp.quotation.changed.v1 and erp.sales_order.changed.v1 (erp/sales' contract).
type document struct {
	ID             string  `json:"id"`
	Number         string  `json:"number"`
	State          string  `json:"state"`
	Currency       string  `json:"currency"`
	OpportunityRef *string `json:"opportunity_ref"`
	QuoteRequestID *string `json:"quote_request_id"`
	Totals         struct {
		Net string `json:"net"`
	} `json:"totals"`
}

// OnSales is OP08, in the subscription's transaction (no call to any component): a quotation or an order that carries
// an opportunity of this component is linked by its version; a quotation answers its request; a newer version may win
// the opportunity by the deployment's setting. erp/sales' refusal of a request marks it rejected.
func OnSales(m *core.Base, ctx context.Context, tx db.Tx, e events.Received) error {
	if e.Type == core.QuoteRequestRejected {
		return rejected(ctx, tx, e)
	}
	kind := KindQuotation
	if e.Type == core.OrderChanged {
		kind = KindOrder
	}
	var d document
	if err := json.Unmarshal(e.Data, &d); err != nil || !core.UUIDForm.MatchString(d.ID) {
		m.RT.Logger.Warn("a sales document that cannot be read is dropped", "event", e.ID, "type", e.Type, "error", err)
		return nil
	}
	if d.OpportunityRef == nil {
		return nil // a document made by hand
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM opportunity WHERE id::text = $1)`, *d.OpportunityRef).
		Scan(&exists); err != nil || !exists {
		if err == nil {
			m.RT.Logger.Warn("a sales document names an opportunity this component does not have", "event", e.ID,
				"opportunity", *d.OpportunityRef)
		}
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO sales_link (opportunity_id, kind, document_id, number, state, net, currency, version)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8)
		ON CONFLICT (kind, document_id) DO UPDATE SET number = EXCLUDED.number, state = EXCLUDED.state,
			net = EXCLUDED.net, currency = EXCLUDED.currency, version = EXCLUDED.version
		WHERE sales_link.version < EXCLUDED.version`, *d.OpportunityRef, kind, d.ID, d.Number, d.State, d.Totals.Net,
		d.Currency, e.Version)
	if err != nil || tag.RowsAffected() == 0 {
		return err // an older version, or the same again: nothing new
	}
	if kind == KindQuotation && d.QuoteRequestID != nil {
		if _, err := tx.Exec(ctx, `UPDATE quote_request SET state = 'drafted', quotation_id = $2
			WHERE id::text = $1 AND opportunity_id = $3 AND state = 'requested'`, *d.QuoteRequestID, d.ID,
			*d.OpportunityRef); err != nil {
			return err
		}
	}
	if !AutoWins(m.AutoWin, kind, d.State) {
		return nil
	}
	o, err := opportunities.Get(ctx, tx, *d.OpportunityRef, true)
	if err != nil {
		return err
	}
	if opportunities.PhaseOf(o.Stage) != opportunities.PhaseOpen {
		return nil // won already, or lost: only linked (a person decides whether to reopen)
	}
	trig := opportunities.Trigger{Kind: kind, DocumentID: &d.ID}
	_, err = opportunities.ChangeIn(m, ctx, tx, o.ID, opportunities.CmdWin, core.OpClose, "", nil,
		func(ctx context.Context, tx db.Tx, o *opportunities.Opportunity) error {
			amount, why := d.Totals.Net, (*string)(nil)
			if d.Currency != o.Currency {
				note := "won by " + kind + " " + d.Number + " in " + d.Currency + ": the expected amount is kept"
				amount, why = o.Amount, &note
			}
			return opportunities.Win(m, ctx, tx, o, amount, why, "", trig)
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// rejected marks a request erp/sales refused (erp.quote_request.rejected.v1; the fact ID is the request's).
func rejected(ctx context.Context, tx db.Tx, e events.Received) error {
	var r struct {
		FactID string `json:"fact_id"`
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(e.Data, &r); err != nil || !core.UUIDForm.MatchString(r.FactID) {
		return nil
	}
	why := r.Reason
	if r.Detail != "" {
		why += ": " + r.Detail
	}
	_, err := tx.Exec(ctx, `UPDATE quote_request SET state = 'rejected', reason = $2 WHERE id::text = $1 AND state = 'requested'`,
		r.FactID, why)
	return err
}
