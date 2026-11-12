package masterdata

import (
	"context"
	"encoding/json"
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/errs"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	"google.golang.org/grpc"
)

// Every call here runs outside a transaction (protocol P4.20): evidence first, then the transaction. Unreachable is
// never "does not exist" (protocol 07): it answers DEPENDENCY_UNAVAILABLE.

func conn(m *core.Base, id string) (*grpc.ClientConn, error) {
	c, ok := m.RT.GRPC(id)
	if !ok {
		return nil, errs.New(errs.ReasonDependencyUnavailable, id+" is not reachable")
	}
	return c, nil
}

// LegalEntity is what an opportunity needs of its legal entity: its code (the number), base currency and time zone.
type LegalEntity struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	State        string `json:"state"`
	Timezone     string `json:"timezone"`
	BaseCurrency string `json:"base_currency"`
}

// LegalEntityOf reads an active legal entity from mdm/org (OP01); none, or not active, answers REFERENCE_INVALID.
func LegalEntityOf(m *core.Base, ctx context.Context, id string) (LegalEntity, error) {
	c, err := conn(m, core.Org)
	if err != nil {
		return LegalEntity{}, err
	}
	res, err := orgv3.NewSourceServiceClient(c).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return LegalEntity{}, err
	}
	var le LegalEntity
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return le, core.ErrReference.New("no legal entity " + id)
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return le, err
	}
	if le.State != "active" {
		return le, core.ErrReference.New("the legal entity is " + le.State)
	}
	return le, nil
}

// CurrencyScale is an active currency's minor unit (OP01: amounts by it); none, or not active, answers
// REFERENCE_INVALID.
func CurrencyScale(m *core.Base, ctx context.Context, code string) (int32, error) {
	c, err := conn(m, core.Currency)
	if err != nil {
		return 0, err
	}
	res, err := currencyv3.NewSourceServiceClient(c).Read(ctx, &currencyv3.ReadRequest{
		Aggregate: currencyv3.Aggregate_AGGREGATE_CURRENCY, Ids: []string{code}}, calls.Idempotent())
	if err != nil {
		return 0, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != currencyv3.ReadState_READ_STATE_EXISTS {
		return 0, core.ErrReference.New("no currency " + code)
	}
	var cur struct {
		MinorUnit int32 `json:"minor_unit"`
		Active    bool  `json:"active"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &cur); err != nil {
		return 0, err
	}
	if !cur.Active {
		return 0, core.ErrReference.New("currency " + code + " is not active")
	}
	return cur.MinorUnit, nil
}

// Customer is the evidence and the name snapshot of a customer.
type Customer struct {
	ID          string
	Name        string
	EvaluatedAt time.Time
}

// CustomerOf asks mdm/partner whether the partner may be sold to in the legal entity now (protocol 07's eligibility,
// purpose sell) and reads its name; not eligible answers CUSTOMER_NOT_ELIGIBLE with mdm/partner's reason.
func CustomerOf(m *core.Base, ctx context.Context, partner, legalEntity string) (Customer, error) {
	c, err := conn(m, core.Partner)
	if err != nil {
		return Customer{}, err
	}
	asked := time.Now()
	e, err := partnerv3.NewPartnerServiceClient(c).CheckEligibility(ctx, &partnerv3.CheckEligibilityRequest{
		PartnerId: partner, LegalEntityId: legalEntity, Purpose: partnerv3.Purpose_PURPOSE_SELL}, calls.Idempotent())
	if err != nil {
		return Customer{}, err
	}
	if !e.GetEligible() {
		return Customer{}, core.ErrIneligible.New("the customer is not eligible: "+e.GetReason()).With("eligibility", e.GetReason())
	}
	res, err := partnerv3.NewSourceServiceClient(c).Read(ctx, &partnerv3.ReadRequest{
		Aggregate: partnerv3.Aggregate_AGGREGATE_PARTNER, Ids: []string{partner}}, calls.Idempotent())
	if err != nil {
		return Customer{}, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != partnerv3.ReadState_READ_STATE_EXISTS {
		return Customer{}, core.ErrIneligible.New("no partner " + partner)
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &p); err != nil {
		return Customer{}, err
	}
	return Customer{ID: partner, Name: p.Name, EvaluatedAt: asked}, nil
}
