package quotes

import (
	"context"
	"encoding/json"
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	opportunities "github.com/brickKit/crm-opportunity/v3/internal/opportunities"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
)

type requestIn struct {
	Lines *[]opportunities.Line `json:"lines"`
}

// RequestQuote is OP05: in the opportunity's change, a quote request and its fact crm.opportunity.quote_requested.v1
// (the fact ID is the request's); lines given replace the opportunity's. The same Idempotency-Key answers the same
// request. erp/sales makes the draft or refuses (OP08).
func RequestQuote(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in requestIn
	if err := core.BindOptional(c, &in); err != nil {
		return err
	}
	if in.Lines != nil {
		if err := opportunities.CheckLines(*in.Lines); err != nil {
			return err
		}
	}
	cur, err := opportunities.Load(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpEdit, core.Facts(cur.LegalEntityID, cur.OwnerID))
	if err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		id := ids.New().String()
		_, err := opportunities.ChangeIn(m, ctx, tx, cur.ID, opportunities.CmdRequestQuote, core.OpEdit, actor, nil,
			func(ctx context.Context, tx db.Tx, o *opportunities.Opportunity) error {
				if in.Lines != nil {
					if err := opportunities.WriteLines(ctx, tx, o.ID, *in.Lines); err != nil {
						return err
					}
					o.Lines = *in.Lines
				}
				lines := o.Lines
				if lines == nil {
					lines = []opportunities.Line{}
				}
				b, err := json.Marshal(lines)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO quote_request (id, opportunity_id, lines, requested_by)
					VALUES ($1, $2, $3, $4)`, id, o.ID, b, actor); err != nil {
					return err
				}
				return core.Emit(m, ctx, tx, core.QuoteRequested, id, 1, o.LegalEntityID, map[string]any{"fact_id": id,
					"opportunity_id": o.ID, "opportunity_number": o.Number, "opportunity_name": o.Name,
					"legal_entity_id": o.LegalEntityID, "customer_id": o.Customer.PartnerID, "currency": o.Currency,
					"owner_id": o.OwnerID, "lines": lines, "requested_at": canon.Time(time.Now())})
			})
		if err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), nil
	})
	if err != nil {
		return err
	}
	var q opportunities.QuoteRequest
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		r, err := opportunities.ReadOf(ctx, tx, cur)
		if err != nil {
			return err
		}
		for _, x := range r.QuoteRequests {
			if x.ID == core.ResultID(out) {
				q = x
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.JSON(200, q)
}
