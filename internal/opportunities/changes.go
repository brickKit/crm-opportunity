package opportunities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	masterdata "github.com/brickKit/crm-opportunity/v3/internal/masterdata"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/search"
)

// actorOf is the acting principal of a user's request ("" for none).
func actorOf(ctx context.Context) string {
	u, _ := identity.UserFrom(ctx)
	return u.Subject
}

const searchVersion = search.Version

func searchText(o Opportunity) string { return search.Text(o.Number, o.Name) }

// work is what a command does to the locked opportunity in its transaction: it changes o and records what it moved;
// run saves o and publishes its state.
type work = func(ctx context.Context, tx db.Tx, o *Opportunity) error

// Run is every change of an existing opportunity: op decided on its facts, the lifecycle's cell for cmd in its phase
// (refused, ignored, or done), then in one transaction the row locked, the cell decided again on the locked state, the
// version checked, the work, the save, the changed event and the audit (protocol 03).
func Run(m *core.Base, c *httpx.Context, cmd, op string, version *int64, f work) error {
	cur, err := Load(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, op, facts(cur))
	if err != nil {
		return err
	}
	return change(m, ctx, cur.ID, cmd, op, actor, version, f, func(o Opportunity) error { return answer(m, c, ctx, o.ID) })
}

// change is run's transaction: done answers the opportunity as it ends.
func change(m *core.Base, ctx context.Context, id, cmd, op, actor string, version *int64, f work,
	done func(Opportunity) error) error {
	var out Opportunity
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		out, err = ChangeIn(m, ctx, tx, id, cmd, op, actor, version, f)
		return err
	})
	if err != nil {
		return err
	}
	return done(out)
}

// ChangeIn is one change in the caller's transaction (also the changes no user makes, OP08): the row locked, the cell
// of cmd decided on the locked state (ignored: the opportunity as it is; refused: STATE_CONFLICT), the version checked
// when given, the work, the save, the changed event and the audit of op (actor "" when no user acts).
func ChangeIn(m *core.Base, ctx context.Context, tx db.Tx, id, cmd, op, actor string, version *int64,
	f func(ctx context.Context, tx db.Tx, o *Opportunity) error) (Opportunity, error) {
	o, err := Get(ctx, tx, id, true)
	if err != nil {
		return o, err
	}
	d := Lifecycle.Decide(PhaseOf(o.Stage), cmd)
	switch d.Effect {
	case core.Ignore:
		return o, nil
	case core.Refuse:
		return o, core.Refused(d, o.Stage)
	}
	if version != nil && *version != o.Version {
		return o, core.ErrVersion.New(fmt.Sprintf("the opportunity is at version %d", o.Version))
	}
	if err := f(ctx, tx, &o); err != nil {
		return o, err
	}
	out, err := save(m, ctx, tx, o)
	if err != nil {
		return out, err
	}
	return out, core.Change(m, ctx, tx, actor, op, out.ID, out)
}

// save writes o's changeable columns, a new version, and publishes its state.
func save(m *core.Base, ctx context.Context, tx db.Tx, o Opportunity) (Opportunity, error) {
	if _, err := tx.Exec(ctx, `UPDATE opportunity SET customer_id = $2, customer_name = $3, name = $4, currency = $5,
		amount = $6::numeric, expected_close_date = $7::date, owner_id = $8, stage = $9, probability = $10,
		stage_before_close = $11, probability_before_close = $12, close_reason = $13, closed_at = $14::timestamptz,
		won_amount = $15::numeric, search = $16, search_version = $17, version = version + 1, updated_at = now()
		WHERE id = $1`, o.ID, o.Customer.PartnerID, o.Customer.Name, o.Name, o.Currency, o.Amount, o.ExpectedCloseDate,
		o.OwnerID, o.Stage, o.Probability, o.StageBeforeClose, o.probabilityBeforeClose, o.CloseReason, o.ClosedAt,
		o.WonAmount, searchText(o), searchVersion); err != nil {
		return o, err
	}
	saved, err := Get(ctx, tx, o.ID, false)
	if err != nil {
		return saved, err
	}
	return saved, core.Emit(m, ctx, tx, core.Changed, saved.ID, saved.Version, saved.LegalEntityID, saved)
}

func reasonOf(field string, r *string) (*string, error) {
	if r == nil || strings.TrimSpace(*r) == "" {
		return nil, core.Invalid(field, "it says why")
	}
	s := strings.TrimSpace(*r)
	return &s, nil
}

// --- OP02: edit ---

// optDate is a date that may be given as null (cleared) or left out (kept).
type optDate struct {
	set bool
	v   *string
}

func (d *optDate) UnmarshalJSON(b []byte) error {
	d.set = true
	return json.Unmarshal(b, &d.v)
}

type editIn struct {
	Version           *int64  `json:"version"`
	Name              *string `json:"name"`
	CustomerID        *string `json:"customer_id"`
	Currency          *string `json:"currency"`
	Amount            *string `json:"amount"`
	ExpectedCloseDate optDate `json:"expected_close_date"`
	Probability       *int    `json:"probability"`
	Lines             *[]Line `json:"lines"`
}

// Edit is OP02: the given fields by version; a won opportunity keeps its amount, currency, customer, lines and
// probability (WON_AMOUNT_LOCKED), a lost one changes after it is reopened (the table).
func Edit(m *core.Base, c *httpx.Context) error {
	var in editIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Version == nil {
		return core.Invalid("version", "a change names the version it changes")
	}
	switch {
	case in.Name != nil:
		if err := checkName(*in.Name); err != nil {
			return err
		}
	}
	if in.CustomerID != nil && !core.UUIDForm.MatchString(*in.CustomerID) {
		return core.Invalid("customer_id", "customer_id is a partner's ID")
	}
	if in.Currency != nil && !core.CurrencyForm.MatchString(*in.Currency) {
		return core.Invalid("currency", "currency is an ISO 4217 code")
	}
	if in.ExpectedCloseDate.v != nil && !core.DateForm.MatchString(*in.ExpectedCloseDate.v) {
		return core.Invalid("expected_close_date", "expected_close_date is YYYY-MM-DD")
	}
	if err := checkProbability(in.Probability); err != nil {
		return err
	}
	if in.Lines != nil {
		if err := CheckLines(*in.Lines); err != nil {
			return err
		}
	}
	cur, err := Load(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpEdit, facts(cur))
	if err != nil {
		return err
	}
	locked := in.CustomerID != nil || in.Currency != nil || in.Amount != nil || in.Probability != nil || in.Lines != nil
	// the evidence, outside the transaction (the version keeps it the opportunity's)
	var cust *masterdata.Customer
	if in.CustomerID != nil && *in.CustomerID != cur.Customer.PartnerID {
		cu, err := masterdata.CustomerOf(m, ctx, *in.CustomerID, cur.LegalEntityID)
		if err != nil {
			return err
		}
		cust = &cu
	}
	currency, amount := cur.Currency, cur.Amount
	if in.Currency != nil {
		currency = *in.Currency
	}
	if in.Amount != nil {
		amount = *in.Amount
	}
	if in.Currency != nil || in.Amount != nil {
		scale, err := masterdata.CurrencyScale(m, ctx, currency)
		if err != nil {
			return err
		}
		if amount, err = core.Decimal("amount", amount, scale); err != nil {
			return err
		}
	}
	return change(m, ctx, cur.ID, CmdEdit, core.OpEdit, actor, in.Version, func(ctx context.Context, tx db.Tx, o *Opportunity) error {
		if o.Stage == core.StageWon && locked { // on the locked state: the one place of this rule
			return core.ErrWonLocked.New("a won opportunity keeps its amount, currency, customer, lines and probability; reopen it")
		}
		if in.Name != nil {
			o.Name = strings.TrimSpace(*in.Name)
		}
		if cust != nil {
			o.Customer = Customer{PartnerID: cust.ID, Name: cust.Name}
		}
		o.Currency, o.Amount = currency, amount
		if in.ExpectedCloseDate.set {
			o.ExpectedCloseDate = in.ExpectedCloseDate.v
		}
		if in.Probability != nil {
			o.Probability = *in.Probability
		}
		if in.Lines != nil {
			return WriteLines(ctx, tx, o.ID, *in.Lines)
		}
		return nil
	}, func(o Opportunity) error { return answer(m, c, ctx, o.ID) })
}

// --- OP03: advance and revert ---

type stageIn struct {
	Version     *int64  `json:"version"`
	Stage       string  `json:"stage"`
	Probability *int    `json:"probability"`
	Reason      *string `json:"reason"`
}

// Move is OP03: advance to a later open stage (past the ones between) or revert to an earlier one with a reason; the
// probability is the stage's default unless given.
func Move(m *core.Base, cmd string) httpx.Handler {
	return func(c *httpx.Context) error {
		var in stageIn
		if err := c.BindJSON(&in); err != nil {
			return err
		}
		if err := checkStage(in.Stage); err != nil {
			return err
		}
		if err := checkProbability(in.Probability); err != nil {
			return err
		}
		var reason *string
		if cmd == CmdRevert {
			r, err := reasonOf("reason", in.Reason)
			if err != nil {
				return err
			}
			reason = r
		}
		return Run(m, c, cmd, core.OpEdit, in.Version, func(ctx context.Context, tx db.Tx, o *Opportunity) error {
			from, to := stageIndex(o.Stage), stageIndex(in.Stage)
			if cmd == CmdAdvance && to <= from || cmd == CmdRevert && to >= from {
				return core.ErrStageOrder.New(fmt.Sprintf("%s does not %s from %s", in.Stage, cmd, o.Stage))
			}
			prev := o.Stage
			o.Stage, o.Probability = in.Stage, defaultProbability(in.Stage)
			if in.Probability != nil {
				o.Probability = *in.Probability
			}
			return moved(m, ctx, tx, *o, &prev, o.Stage, reason, actorOf(ctx), UserTrigger)
		})
	}
}

// --- OP04: assign ---

type assignIn struct {
	Version *int64 `json:"version"`
	OwnerID string `json:"owner_id"`
}

// Assign is OP04: another operation than editing; the assigner may assign within the new owner's facts too.
func Assign(m *core.Base, c *httpx.Context) error {
	var in assignIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if !core.UUIDForm.MatchString(in.OwnerID) {
		return core.Invalid("owner_id", "owner_id is a principal's ID")
	}
	return Run(m, c, CmdAssign, core.OpAssign, in.Version, func(ctx context.Context, tx db.Tx, o *Opportunity) error {
		o.OwnerID = in.OwnerID
		return nil
	})
}

// --- OP06, OP07: win, lose, reopen ---

type closeIn struct {
	Version   *int64  `json:"version"`
	WonAmount *string `json:"won_amount"`
	Reason    *string `json:"reason"`
}

// Close is OP06 and OP07 by a user: win (the won amount, the expected amount unless given), lose (a reason), reopen
// (a reason; back to the stage and probability before closing).
func Close(m *core.Base, cmd string) httpx.Handler {
	return func(c *httpx.Context) error {
		var in closeIn
		if err := core.BindOptional(c, &in); err != nil {
			return err
		}
		var reason *string
		if cmd == CmdLose || cmd == CmdReopen {
			r, err := reasonOf("reason", in.Reason)
			if err != nil {
				return err
			}
			reason = r
		}
		var scale int32 = -1
		if cmd == CmdWin && in.WonAmount != nil {
			cur, err := Load(m, c.Ctx(), c.Param("id"))
			if err != nil {
				return err
			}
			if scale, err = masterdata.CurrencyScale(m, c.Ctx(), cur.Currency); err != nil {
				return err
			}
		}
		return Run(m, c, cmd, core.OpClose, in.Version, func(ctx context.Context, tx db.Tx, o *Opportunity) error {
			switch cmd {
			case CmdWin:
				amount := o.Amount
				if in.WonAmount != nil {
					a, err := core.Decimal("won_amount", *in.WonAmount, scale)
					if err != nil {
						return err
					}
					amount = a
				}
				return Win(m, ctx, tx, o, amount, nil, actorOf(ctx), UserTrigger)
			case CmdLose:
				return lose(m, ctx, tx, o, *reason, actorOf(ctx))
			default:
				return reopen(m, ctx, tx, o, *reason, actorOf(ctx))
			}
		})
	}
}

// Win closes o as won with its amount (OP06; by a sales document's event, OP08): the stage and probability before
// it kept, the stage change and crm.opportunity.won.v1; why is the stage change's reason (an automatic win's note).
func Win(m *core.Base, ctx context.Context, tx db.Tx, o *Opportunity, amount string, why *string, actor string,
	trig Trigger) error {
	closeAs(o, core.StageWon, nil)
	o.WonAmount = &amount
	prev := *o.StageBeforeClose
	if err := moved(m, ctx, tx, *o, &prev, core.StageWon, why, actor, trig); err != nil {
		return err
	}
	fact := ids.New().String()
	return core.Emit(m, ctx, tx, core.Won, fact, 1, o.LegalEntityID, map[string]any{"fact_id": fact, "opportunity_id": o.ID,
		"legal_entity_id": o.LegalEntityID, "owner_id": o.OwnerID, "customer_id": o.Customer.PartnerID,
		"won_amount": amount, "currency": o.Currency, "trigger": trig, "occurred_at": canon.Time(time.Now())})
}

func lose(m *core.Base, ctx context.Context, tx db.Tx, o *Opportunity, reason, actor string) error {
	closeAs(o, core.StageLost, &reason)
	prev := *o.StageBeforeClose
	if err := moved(m, ctx, tx, *o, &prev, core.StageLost, &reason, actor, UserTrigger); err != nil {
		return err
	}
	fact := ids.New().String()
	return core.Emit(m, ctx, tx, core.Lost, fact, 1, o.LegalEntityID, map[string]any{"fact_id": fact, "opportunity_id": o.ID,
		"legal_entity_id": o.LegalEntityID, "owner_id": o.OwnerID, "customer_id": o.Customer.PartnerID, "reason": reason,
		"occurred_at": canon.Time(time.Now())})
}

// closeAs keeps the open stage and probability and closes o as won (100) or lost (0).
func closeAs(o *Opportunity, stage string, reason *string) {
	before, p := o.Stage, o.Probability
	o.StageBeforeClose, o.probabilityBeforeClose = &before, &p
	o.Stage, o.CloseReason = stage, reason
	o.Probability = 0
	if stage == core.StageWon {
		o.Probability = 100
	}
	now := canon.Time(time.Now())
	o.ClosedAt = &now
}

func reopen(m *core.Base, ctx context.Context, tx db.Tx, o *Opportunity, reason, actor string) error {
	prev := o.Stage
	o.Stage, o.Probability = *o.StageBeforeClose, *o.probabilityBeforeClose
	o.StageBeforeClose, o.probabilityBeforeClose, o.CloseReason, o.ClosedAt, o.WonAmount = nil, nil, nil, nil, nil
	return moved(m, ctx, tx, *o, &prev, o.Stage, &reason, actor, UserTrigger)
}
