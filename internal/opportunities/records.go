package opportunities

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/jackc/pgx/v5"
)

// Opportunity is an opportunity's complete state (crm.opportunity.changed.v1).
type Opportunity struct {
	ID                string   `json:"id"`
	Number            string   `json:"number"`
	LegalEntityID     string   `json:"legal_entity_id"`
	Customer          Customer `json:"customer"`
	Name              string   `json:"name"`
	Currency          string   `json:"currency"`
	Amount            string   `json:"amount"`
	ExpectedCloseDate *string  `json:"expected_close_date"`
	OwnerID           string   `json:"owner_id"`
	Stage             string   `json:"stage"`
	Probability       int      `json:"probability"`
	StageBeforeClose  *string  `json:"stage_before_close"`
	CloseReason       *string  `json:"close_reason"`
	ClosedAt          *string  `json:"closed_at"`
	WonAmount         *string  `json:"won_amount"`
	Lines             []Line   `json:"lines"`
	Version           int64    `json:"version"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`

	probabilityBeforeClose *int
}

// Customer is the customer and its name's snapshot.
type Customer struct {
	PartnerID string `json:"partner_id"`
	Name      string `json:"name"`
}

// Line is a product the opportunity suggests for a quotation.
type Line struct {
	ProductID string `json:"product_id"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
}

// Read is what the user surface answers: the state, its stage history, its quote requests and the sales documents
// that came back.
type Read struct {
	Opportunity
	StageHistory   []StageChange  `json:"stage_history"`
	QuoteRequests  []QuoteRequest `json:"quote_requests"`
	SalesDocuments []SalesLink    `json:"sales_documents"`
}

// StageChange is one row of the stage history.
type StageChange struct {
	ID      string  `json:"id"`
	From    *string `json:"from"`
	To      string  `json:"to"`
	Reason  *string `json:"reason"`
	Actor   string  `json:"actor"`
	Trigger Trigger `json:"trigger"`
	At      string  `json:"at"`
}

// Trigger is what caused a change: a user's command or a sales document's event (OP08).
type Trigger struct {
	Kind       string  `json:"kind"`
	DocumentID *string `json:"document_id"`
}

// UserTrigger is a user's command.
var UserTrigger = Trigger{Kind: "user"}

// QuoteRequest is one quote request (OP05).
type QuoteRequest struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Lines       []Line  `json:"lines"`
	QuotationID *string `json:"quotation_id"`
	Reason      *string `json:"reason"`
	RequestedBy string  `json:"requested_by"`
	RequestedAt string  `json:"requested_at"`
}

// SalesLink is an erp/sales quotation or order that carries the opportunity (OP08).
type SalesLink struct {
	Kind       string `json:"kind"`
	DocumentID string `json:"document_id"`
	Number     string `json:"number"`
	State      string `json:"state"`
	Net        string `json:"net"`
	Currency   string `json:"currency"`
	Version    int64  `json:"version"`
}

// Cols are an opportunity's columns as Scan reads them (alias o).
const Cols = `o.id::text, o.number, o.legal_entity_id::text, o.customer_id::text, o.customer_name, o.name, o.currency,
	trim_scale(o.amount)::text, o.expected_close_date::text, o.owner_id, o.stage, o.probability, o.stage_before_close,
	o.probability_before_close, o.close_reason, o.closed_at, trim_scale(o.won_amount)::text, o.version, o.created_at,
	o.updated_at, COALESCE((SELECT jsonb_agg(jsonb_build_object('product_id', l.product_id::text,
		'quantity', trim_scale(l.quantity)::text, 'unit', l.unit) ORDER BY l.line_no) FROM opportunity_line l
		WHERE l.opportunity_id = o.id), '[]'::jsonb)`

// Scan reads one row of Cols.
func Scan(row pgx.Row) (Opportunity, error) {
	var o Opportunity
	var created, updated time.Time
	var closed *time.Time
	var lines []byte
	err := row.Scan(&o.ID, &o.Number, &o.LegalEntityID, &o.Customer.PartnerID, &o.Customer.Name, &o.Name, &o.Currency,
		&o.Amount, &o.ExpectedCloseDate, &o.OwnerID, &o.Stage, &o.Probability, &o.StageBeforeClose,
		&o.probabilityBeforeClose, &o.CloseReason, &closed, &o.WonAmount, &o.Version, &created, &updated, &lines)
	if err != nil {
		return o, err
	}
	o.CreatedAt, o.UpdatedAt = canon.Time(created), canon.Time(updated)
	if closed != nil {
		s := canon.Time(*closed)
		o.ClosedAt = &s
	}
	return o, json.Unmarshal(lines, &o.Lines)
}

// errNone is a lookup that found no row.
var errNone = errors.New("no such opportunity")

// Get reads an opportunity in tx; lock takes its row's lock, the one every change of it takes.
func Get(ctx context.Context, tx db.Tx, id string, lock bool) (Opportunity, error) {
	if lock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM opportunity WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
			return Opportunity{}, err
		}
	}
	o, err := Scan(tx.QueryRow(ctx, `SELECT `+Cols+` FROM opportunity o WHERE o.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, errNone
	}
	return o, err
}

// Load reads an opportunity outside any change; none (or an ID of the wrong form) answers NOT_FOUND.
func Load(m *core.Base, ctx context.Context, id string) (Opportunity, error) {
	var o Opportunity
	if !core.UUIDForm.MatchString(id) {
		return o, core.NotFound("opportunity")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		o, err = Get(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, errNone) {
		return o, core.NotFound("opportunity")
	}
	return o, err
}

// ReadOf is the opportunity with its history, requests and sales documents.
func ReadOf(ctx context.Context, tx db.Tx, o Opportunity) (Read, error) {
	r := Read{Opportunity: o, StageHistory: []StageChange{}, QuoteRequests: []QuoteRequest{}, SalesDocuments: []SalesLink{}}
	rows, err := tx.Query(ctx, `SELECT id::text, from_stage, to_stage, reason, actor, trigger_kind, trigger_id::text, at
		FROM stage_change WHERE opportunity_id = $1 ORDER BY at, id`, o.ID)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var s StageChange
		var at time.Time
		if err := rows.Scan(&s.ID, &s.From, &s.To, &s.Reason, &s.Actor, &s.Trigger.Kind, &s.Trigger.DocumentID, &at); err != nil {
			rows.Close()
			return r, err
		}
		s.At = canon.Time(at)
		r.StageHistory = append(r.StageHistory, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	rows, err = tx.Query(ctx, `SELECT id::text, state, lines, quotation_id::text, reason, requested_by, requested_at
		FROM quote_request WHERE opportunity_id = $1 ORDER BY requested_at, id`, o.ID)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var q QuoteRequest
		var lines []byte
		var at time.Time
		if err := rows.Scan(&q.ID, &q.State, &lines, &q.QuotationID, &q.Reason, &q.RequestedBy, &at); err != nil {
			rows.Close()
			return r, err
		}
		q.RequestedAt = canon.Time(at)
		if err := json.Unmarshal(lines, &q.Lines); err != nil {
			rows.Close()
			return r, err
		}
		r.QuoteRequests = append(r.QuoteRequests, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	rows, err = tx.Query(ctx, `SELECT kind, document_id::text, number, state, trim_scale(net)::text, currency, version
		FROM sales_link WHERE opportunity_id = $1 ORDER BY kind, number`, o.ID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SalesLink
		if err := rows.Scan(&s.Kind, &s.DocumentID, &s.Number, &s.State, &s.Net, &s.Currency, &s.Version); err != nil {
			return r, err
		}
		r.SalesDocuments = append(r.SalesDocuments, s)
	}
	return r, rows.Err()
}
