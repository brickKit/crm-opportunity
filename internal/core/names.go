package core

import (
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
)

// ID is the component's ID.
const ID = "crm/opportunity"

// Dependencies (the IDs runtime.GRPC takes).
const (
	Partner  = "mdm/partner"
	Org      = "mdm/org"
	Currency = "mdm/currency"
	// Sales answers quote requests by events only: not a dependency (CRM and ERP only meet by events).
	Sales = "erp/sales"
)

// The stream and its event types (contract/events), and the ones subscribed to.
const (
	Stream         = "crm.opportunity"
	Changed        = "crm.opportunity.changed.v1"
	StageChanged   = "crm.opportunity.stage_changed.v1"
	QuoteRequested = "crm.opportunity.quote_requested.v1"
	Won            = "crm.opportunity.won.v1"
	Lost           = "crm.opportunity.lost.v1"
	// erp/sales' stream (OP08)
	SalesStream          = "erp.sales"
	QuotationChanged     = "erp.quotation.changed.v1"
	OrderChanged         = "erp.sales_order.changed.v1"
	QuoteRequestRejected = "erp.quote_request.rejected.v1"
)

// Resources and operations (contract/openapi.yaml names each route's operation).
const (
	Opportunities = "crm.opportunity"
	OpRead        = "crm.opportunity.read"
	OpEdit        = "crm.opportunity.edit"
	OpClose       = "crm.opportunity.close"
	OpAssign      = "crm.opportunity.assign"
	System        = "crm.opportunity.system"
	// OpResend is the redelivery of this component's stream (be-protocol P6.38).
	OpResend = "crm.opportunity.system.resend"
	// OpRecover is a full recovery's checks (be-protocol chapter 14: the recovery tool only).
	OpRecover = "crm.opportunity.system.recover"
)

// Dimensions (security 07): the legal entity and the owner.
const (
	DimLegalEntity = "legal_entity"
	DimOwner       = "owner"
)

// The stages of the one pipeline of this version, in their order; won and lost close an opportunity (OP03, OP06).
const (
	StageQualification = "qualification"
	StageNeedsAnalysis = "needs_analysis"
	StageProposal      = "proposal"
	StageNegotiation   = "negotiation"
	StageWon           = "won"
	StageLost          = "lost"
)

// OpenStages are the stages in progress in their order, with their default probability.
var OpenStages = []struct {
	Name        string
	Probability int
}{{StageQualification, 10}, {StageNeedsAnalysis, 25}, {StageProposal, 50}, {StageNegotiation, 75}}

// Parameters (protocol 14).
const (
	MaxLines   = 100
	MaxNameLen = 200
)

func fields(names ...string) map[string]sdkauthz.Field {
	out := map[string]sdkauthz.Field{}
	for _, n := range names {
		out[n] = sdkauthz.Field{Class: sdkauthz.Ordinary}
	}
	return out
}

// OpportunityFields are what an opportunity list may filter and sort on.
var OpportunityFields = []string{"id", "number", "legal_entity_id", "customer_id", "customer_name", "name", "currency",
	"amount", "expected_close_date", "owner_id", "stage", "probability", "won_amount", "closed_at", "version",
	"created_at", "updated_at"}

// Definitions are the component's resources and operations.
func Definitions() *sdkauthz.Definitions {
	op := func(key, purpose, risk string, audit bool) sdkauthz.Operation {
		o := sdkauthz.Operation{Key: key, ResourceType: Opportunities, Purpose: purpose, RiskClass: risk, Audit: audit}
		if purpose != "read" {
			o.SideEffects = []string{"business_write"}
		}
		return o
	}
	return &sdkauthz.Definitions{Component: ID,
		Resources: []sdkauthz.Resource{
			{Type: Opportunities, ViewOperation: OpRead, Fields: fields(OpportunityFields...),
				Dimensions: map[string]sdkauthz.Dimension{
					DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"},
					DimOwner:       {Kind: sdkauthz.Principal, Column: "owner_id", PrincipalRole: "owner"}}},
			{Type: System, ViewOperation: OpRecover, Dimensions: map[string]sdkauthz.Dimension{},
				Fields: map[string]sdkauthz.Field{}},
		},
		Operations: []sdkauthz.Operation{
			op(OpRead, "read", "low", false),
			op(OpEdit, "manage", "low", true),
			op(OpClose, "action", "low", true),
			op(OpAssign, "action", "low", true),
			{Key: OpResend, ResourceType: System, Purpose: "read", RiskClass: "low"},
			{Key: OpRecover, ResourceType: System, Purpose: "read", RiskClass: "low"},
		}}
}
