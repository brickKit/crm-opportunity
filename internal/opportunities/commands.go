package opportunities

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	masterdata "github.com/brickKit/crm-opportunity/v3/internal/masterdata"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/search"
)

func facts(o Opportunity) sdkauthz.Facts { return core.Facts(o.LegalEntityID, o.OwnerID) }

// stageIndex is a stage's place in the pipeline: the open stages 0-3, -1 for won, lost or an unknown name.
func stageIndex(stage string) int {
	for i, s := range core.OpenStages {
		if s.Name == stage {
			return i
		}
	}
	return -1
}

// defaultProbability is an open stage's default probability.
func defaultProbability(stage string) int { return core.OpenStages[stageIndex(stage)].Probability }

// checkStage answers an open stage, or the refusal: won and lost are reached only by winning and losing (OP03).
func checkStage(stage string) error {
	switch {
	case stage == core.StageWon || stage == core.StageLost:
		return core.ErrStageOrder.New(stage + " is reached by winning or losing, not by moving stages")
	case stageIndex(stage) < 0:
		return core.Invalid("stage", "stage is one of qualification, needs_analysis, proposal, negotiation")
	}
	return nil
}

func checkProbability(p *int) error {
	if p != nil && (*p < 0 || *p > 100) {
		return core.Invalid("probability", "probability is 0-100")
	}
	return nil
}

// CheckLines checks the products an opportunity suggests.
func CheckLines(lines []Line) error {
	if len(lines) > core.MaxLines {
		return core.Invalid("lines", fmt.Sprintf("an opportunity has at most %d lines", core.MaxLines))
	}
	for i, l := range lines {
		field := fmt.Sprintf("lines[%d]", i)
		if !core.UUIDForm.MatchString(l.ProductID) {
			return core.Invalid(field+".product_id", "product_id is a product's ID")
		}
		if !core.DecimalForm.MatchString(l.Quantity) || strings.Trim(strings.ReplaceAll(l.Quantity, ".", ""), "0") == "" {
			return core.Invalid(field+".quantity", "quantity is a decimal string above zero")
		}
		if strings.TrimSpace(l.Unit) == "" {
			return core.Invalid(field+".unit", "unit is the product's unit")
		}
	}
	return nil
}

func checkName(name string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); n == 0 || n > core.MaxNameLen {
		return core.Invalid("name", fmt.Sprintf("name is 1-%d characters", core.MaxNameLen))
	}
	return nil
}

type createIn struct {
	LegalEntityID     string  `json:"legal_entity_id"`
	CustomerID        string  `json:"customer_id"`
	Name              string  `json:"name"`
	Currency          *string `json:"currency"`
	Amount            *string `json:"amount"`
	ExpectedCloseDate *string `json:"expected_close_date"`
	OwnerID           *string `json:"owner_id"`
	Stage             *string `json:"stage"`
	Probability       *int    `json:"probability"`
	Lines             []Line  `json:"lines"`
}

// Create is OP01: the evidence of the legal entity, the currency and the customer, then the opportunity, its first
// stage change and its events in one transaction; an owner other than the caller needs the assign operation (OP04).
func Create(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in createIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	stage := core.StageQualification
	if in.Stage != nil {
		stage = *in.Stage
	}
	switch {
	case !core.UUIDForm.MatchString(in.LegalEntityID):
		return core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !core.UUIDForm.MatchString(in.CustomerID):
		return core.Invalid("customer_id", "customer_id is a partner's ID")
	case in.Currency != nil && !core.CurrencyForm.MatchString(*in.Currency):
		return core.Invalid("currency", "currency is an ISO 4217 code")
	case in.ExpectedCloseDate != nil && !core.DateForm.MatchString(*in.ExpectedCloseDate):
		return core.Invalid("expected_close_date", "expected_close_date is YYYY-MM-DD")
	case in.OwnerID != nil && !core.UUIDForm.MatchString(*in.OwnerID):
		return core.Invalid("owner_id", "owner_id is a principal's ID")
	case stageIndex(stage) < 0:
		return core.Invalid("stage", "a new opportunity is in a stage in progress")
	}
	for _, err := range []error{checkName(in.Name), checkProbability(in.Probability), CheckLines(in.Lines)} {
		if err != nil {
			return err
		}
	}
	owner := actorOf(c.Ctx())
	if in.OwnerID != nil {
		owner = *in.OwnerID
	}
	ctx, actor, err := core.Check(c, core.OpEdit, core.Facts(in.LegalEntityID, owner))
	if err != nil {
		return err
	}
	if owner != actor {
		if _, _, err := core.Check(c, core.OpAssign, core.Facts(in.LegalEntityID, owner)); err != nil {
			return err
		}
	}
	le, err := masterdata.LegalEntityOf(m, ctx, in.LegalEntityID)
	if err != nil {
		return err
	}
	currency := le.BaseCurrency
	if in.Currency != nil {
		currency = *in.Currency
	}
	scale, err := masterdata.CurrencyScale(m, ctx, currency)
	if err != nil {
		return err
	}
	amount := "0"
	if in.Amount != nil {
		if amount, err = core.Decimal("amount", *in.Amount, scale); err != nil {
			return err
		}
	}
	cust, err := masterdata.CustomerOf(m, ctx, in.CustomerID, in.LegalEntityID)
	if err != nil {
		return err
	}
	today, err := bizdate.Today(le.Timezone)
	if err != nil {
		return err
	}
	number, err := core.Number(m, ctx, le.ID, le.Code, today)
	if err != nil {
		return err
	}
	probability := defaultProbability(stage)
	if in.Probability != nil {
		probability = *in.Probability
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO opportunity (id, number, legal_entity_id, customer_id, customer_name, name,
			currency, amount, expected_close_date, owner_id, stage, probability, search, search_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::numeric, $9::date, $10, $11, $12, $13, $14)`, id, number, le.ID, cust.ID,
			cust.Name, strings.TrimSpace(in.Name), currency, amount, in.ExpectedCloseDate, owner, stage, probability,
			search.Text(number, in.Name), search.Version); err != nil {
			return idem.Outcome{}, err
		}
		if err := WriteLines(ctx, tx, id, in.Lines); err != nil {
			return idem.Outcome{}, err
		}
		o, err := Get(ctx, tx, id, false)
		if err != nil {
			return idem.Outcome{}, err
		}
		if err := moved(m, ctx, tx, o, nil, stage, nil, actor, UserTrigger); err != nil {
			return idem.Outcome{}, err
		}
		if err := core.Emit(m, ctx, tx, core.Changed, o.ID, o.Version, o.LegalEntityID, o); err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpEdit, o.ID, o)
	})
	if err != nil {
		return err
	}
	return answer(m, c, ctx, core.ResultID(out))
}

// WriteLines replaces an opportunity's lines in tx.
func WriteLines(ctx context.Context, tx db.Tx, id string, lines []Line) error {
	if _, err := tx.Exec(ctx, `DELETE FROM opportunity_line WHERE opportunity_id = $1`, id); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO opportunity_line (opportunity_id, line_no, product_id, quantity, unit)
			VALUES ($1, $2, $3, $4::numeric, $5)`, id, i+1, l.ProductID, l.Quantity, strings.TrimSpace(l.Unit)); err != nil {
			return err
		}
	}
	return nil
}

// moved records a stage change and publishes it (crm.opportunity.stage_changed.v1; the fact ID is the change's).
func moved(m *core.Base, ctx context.Context, tx db.Tx, o Opportunity, from *string, to string, reason *string, actor string,
	trig Trigger) error {
	id := ids.New().String()
	who := actor
	if who == "" {
		who = "system"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stage_change (id, opportunity_id, from_stage, to_stage, reason, actor,
		trigger_kind, trigger_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, o.ID, from, to, reason, who, trig.Kind,
		trig.DocumentID); err != nil {
		return err
	}
	return core.Emit(m, ctx, tx, core.StageChanged, id, 1, o.LegalEntityID, map[string]any{"fact_id": id,
		"opportunity_id": o.ID, "legal_entity_id": o.LegalEntityID, "owner_id": o.OwnerID, "from": from, "to": to,
		"reason": reason, "trigger": trig, "occurred_at": canon.Time(time.Now())})
}

// answer reads the opportunity back with its history, for the caller who just changed it.
func answer(m *core.Base, c *httpx.Context, ctx context.Context, id string) error {
	var out Read
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		o, err := Get(ctx, tx, id, false)
		if err != nil {
			return err
		}
		out, err = ReadOf(ctx, tx, o)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// GetOpportunity reads one opportunity the caller may see.
func GetOpportunity(m *core.Base, c *httpx.Context) error {
	o, err := Load(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, _, err := core.Check(c, core.OpRead, facts(o))
	if err != nil {
		return err
	}
	return answer(m, c, ctx, o.ID)
}
