package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/money"
)

// The component's own reasons (the SDK's catalog has the shared ones, e.g. REQUEST_INVALID, DEPENDENCY_UNAVAILABLE).
var (
	ErrVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	ErrState   = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The record's state does not allow this")
	// OP03: a stage not after (advance) or before (revert) the current one, or won and lost, which only win and lose
	// reach
	ErrStageOrder = errs.Define(ID, "STAGE_ORDER", errs.FailedPrecondition, 409, errs.RetryNo,
		"Stages move forward in their order, or back explicitly")
	// OP02: a won opportunity's amount, currency, customer and lines are fixed; reopen it to change them
	ErrWonLocked = errs.Define(ID, "WON_AMOUNT_LOCKED", errs.FailedPrecondition, 409, errs.RetryNo,
		"A won opportunity's amount is fixed; reopen it to change it")
	// OP01, OP02: mdm/partner's eligibility for selling in the legal entity said no
	ErrIneligible = errs.Define(ID, "CUSTOMER_NOT_ELIGIBLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The customer may not be sold to in this legal entity")
	// OP01, OP02: the legal entity or the currency does not exist or is not active
	ErrReference = errs.Define(ID, "REFERENCE_INVALID", errs.FailedPrecondition, 409, errs.RetryNo,
		"The legal entity or the currency does not exist or is not active")
)

var (
	UUIDForm     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	CurrencyForm = regexp.MustCompile(`^[A-Z]{3}$`)
	DateForm     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	DecimalForm  = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

// Facts are an opportunity's facts on its two dimensions.
func Facts(legalEntity, owner string) sdkauthz.Facts {
	return sdkauthz.Facts{DimLegalEntity: sdkauthz.Value(legalEntity), DimOwner: sdkauthz.Value(owner)}
}

// Number takes an opportunity number of a series that allows gaps: OP-<legal entity code>-<year>-<six digits>.
func Number(m *Base, ctx context.Context, legalEntity, code, date string) (string, error) {
	year := date[:4]
	n, err := m.RT.DB.NextGappedNumber(ctx, db.Series{LegalEntity: legalEntity, Code: "OP", Period: year})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("OP-%s-%s-%06d", code, year, n), nil
}

// BindOptional decodes a body the contract makes optional; no body leaves v as it is.
func BindOptional(c *httpx.Context, v any) error {
	body, err := c.Body()
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return err
	}
	return c.BindJSON(v)
}

// IDResult is what an idempotent command keeps (the record's ID); ResultID reads it back on a replay too.
func IDResult(id string) idem.Outcome {
	b, _ := json.Marshal(map[string]string{"id": id})
	return idem.Outcome{Status: 200, Result: b}
}

func ResultID(out idem.Outcome) string {
	var r struct{ ID string }
	_ = json.Unmarshal(out.Result, &r)
	return r.ID
}

// Check decides op on a record with the given facts and returns the context to work in and the acting principal.
func Check(c *httpx.Context, op string, facts sdkauthz.Facts) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, facts)
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func Invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func NotFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// Unsupported answers a route of the contract this version does not serve yet (CAPABILITY_UNSUPPORTED, D75).
func Unsupported(what string) httpx.Handler {
	return func(c *httpx.Context) error {
		return errs.New(errs.ReasonCapabilityUnsupported, what+" is not in this version of crm/opportunity")
	}
}

// Decimal checks a non-negative decimal string of at most scale places and returns its canonical form.
func Decimal(field, v string, scale int32) (string, error) {
	d, err := money.Parse(v)
	if err != nil || d.Sign() < 0 || -d.Exponent > scale {
		return "", Invalid(field, field+" is a non-negative decimal string with at most "+strconv.Itoa(int(scale))+" places")
	}
	d.Reduce(d)
	return d.Text('f'), nil
}

// Change records the business change with the record's new state (protocol 03), in the transaction that made it;
// an automatic win has no acting user (actor "").
func Change(m *Base, ctx context.Context, tx db.Tx, actor, op, id string, after any) error {
	_, err := m.RT.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: audit.TargetOf(Opportunities, id, after), Changes: map[string]any{"after": after}})
	return err
}

// Emit writes an event into the outbox, in the transaction that changed the record (protocol 02, S2): a full state
// by its version, or an incremental fact (version 1, its record the fact's ID).
func Emit(m *Base, ctx context.Context, tx db.Tx, typ, id string, version int64, legalEntity string, data any) error {
	_, err := m.RT.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, LegalEntity: legalEntity, Data: data})
	return err
}

// QueryFilters checks the query parameters a list or the funnel filters on before the SDK casts them in SQL (a value
// of the wrong form is the caller's mistake: REQUEST_INVALID, not a failed query): IDs are UUIDs, dates YYYY-MM-DD.
func QueryFilters(c *httpx.Context, uuids, dates []string) error {
	for _, k := range uuids {
		if v := c.Query(k); v != "" && !UUIDForm.MatchString(v) {
			return Invalid(k, k+" is an ID")
		}
	}
	for _, k := range dates {
		if v := c.Query(k); v != "" && !DateForm.MatchString(v) {
			return Invalid(k, k+" is YYYY-MM-DD")
		}
	}
	return nil
}
