package forecast

import (
	"context"
	"fmt"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	masterdata "github.com/brickKit/crm-opportunity/v3/internal/masterdata"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/money"
)

// The funnel and its forecast: the replacement point of the forecast method (docs/design.md, 部署与替换). A fork
// changes weighted (how a row weighs), not the funnel's shape.

// value is an opportunity's amount in the funnel: the won amount once won.
const value = `CASE WHEN o.stage = 'won' THEN o.won_amount ELSE o.amount END`

// weighted is this version's method: the value times the stage's probability (a lost one weighs 0).
const weighted = value + ` * o.probability / 100`

// the funnel's filters are the list's (the same scope, compiled by the SDK into the query).
var funnelSpec = list.Spec{Operation: core.OpRead, Alias: "o", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "owner_id", Column: "owner_id", Kind: list.Text, Filterable: true},
		{Field: "expected_close_date", Column: "expected_close_date", Kind: list.Date, Filterable: true}}}

// Row is one legal entity, currency and stage of the funnel.
type Row struct {
	LegalEntityID string `json:"legal_entity_id"`
	Currency      string `json:"currency"`
	Stage         string `json:"stage"`
	Count         int    `json:"count"`
	Amount        string `json:"amount"`
	Weighted      string `json:"weighted"`
}

// Funnel is the visible opportunities by legal entity, currency and stage: count, value and weighted value, the
// weighted rounded to the currency's minor units; currencies are not converted.
func Funnel(m *core.Base, c *httpx.Context) error {
	if err := core.QueryFilters(c, []string{"legal_entity_id"}, []string{"close_from", "close_to"}); err != nil {
		return err
	}
	var f []list.Filter
	for _, k := range []string{"legal_entity_id", "owner_id"} {
		if v := c.Query(k); v != "" {
			f = append(f, list.Filter{Field: k, Op: "eq", Values: []string{v}})
		}
	}
	if v := c.Query("close_from"); v != "" {
		f = append(f, list.Filter{Field: "expected_close_date", Op: "gte", Values: []string{v}})
	}
	if v := c.Query("close_to"); v != "" {
		f = append(f, list.Filter{Field: "expected_close_date", Op: "lte", Values: []string{v}})
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, funnelSpec, list.Request{Filters: f, Limit: list.MaxLimit})
	if err != nil {
		return err
	}
	rows := []Row{}
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		r, err := tx.Query(ctx, `SELECT o.legal_entity_id::text, o.currency, o.stage, count(*),
			trim_scale(sum(`+value+`))::text, sum(`+weighted+`)::text FROM opportunity o WHERE `+q.Where+`
			GROUP BY 1, 2, 3 ORDER BY 1, 2, array_position(ARRAY['qualification', 'needs_analysis', 'proposal',
			'negotiation', 'won', 'lost'], o.stage)`, q.Args...)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var x Row
			if err := r.Scan(&x.LegalEntityID, &x.Currency, &x.Stage, &x.Count, &x.Amount, &x.Weighted); err != nil {
				return err
			}
			rows = append(rows, x)
		}
		return r.Err()
	})
	if err != nil {
		return err
	}
	if err := round(m, ctx, rows); err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"rows": rows})
}

// round rounds each weighted value to its currency's minor units (protocol 14), outside the transaction.
func round(m *core.Base, ctx context.Context, rows []Row) error {
	scales := map[string]int32{}
	for i, r := range rows {
		scale, ok := scales[r.Currency]
		if !ok {
			s, err := masterdata.CurrencyScale(m, ctx, r.Currency)
			if err != nil {
				return err
			}
			scale, scales[r.Currency] = s, s
		}
		d, err := money.Parse(r.Weighted)
		if err != nil {
			return fmt.Errorf("the funnel's weighted %q: %w", r.Weighted, err)
		}
		r := money.Round(d, scale, money.HalfAwayFromZero)
		r.Reduce(r)
		rows[i].Weighted = r.Text('f')
	}
	return nil
}
