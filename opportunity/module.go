package opportunity

import (
	"context"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	forecast "github.com/brickKit/crm-opportunity/v3/internal/forecast"
	opportunities "github.com/brickKit/crm-opportunity/v3/internal/opportunities"
	quotes "github.com/brickKit/crm-opportunity/v3/internal/quotes"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Module is the opportunity component as the launcher and the shells run it: it only assembles the business modules
// under internal/ (their map is internal/AGENTS.md).
type Module struct {
	b *core.Base
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) {
	b, err := core.NewBase(rt)
	if err != nil {
		return nil, err
	}
	return &Module{b: b}, nil
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	h := func(f func(*core.Base, *httpx.Context) error) httpx.Handler {
		return func(c *httpx.Context) error { return f(m.b, c) }
	}
	o := "/api/opportunity/opportunities"
	add("GET", o, core.OpRead, h(opportunities.ListOpportunities))                                // listOpportunities
	add("POST", o, core.OpEdit, h(opportunities.Create))                                          // createOpportunity
	add("GET", o+"/:id", core.OpRead, h(opportunities.GetOpportunity))                            // getOpportunity
	add("PATCH", o+"/:id", core.OpEdit, h(opportunities.Edit))                                    // updateOpportunity
	add("POST", o+"/:id/advance", core.OpEdit, opportunities.Move(m.b, opportunities.CmdAdvance)) // advanceOpportunity
	add("POST", o+"/:id/revert", core.OpEdit, opportunities.Move(m.b, opportunities.CmdRevert))   // revertOpportunity
	add("POST", o+"/:id/assign", core.OpAssign, h(opportunities.Assign))                          // assignOpportunity
	add("POST", o+"/:id/quote-requests", core.OpEdit, h(quotes.RequestQuote))                     // requestQuote
	add("POST", o+"/:id/win", core.OpClose, opportunities.Close(m.b, opportunities.CmdWin))       // winOpportunity
	add("POST", o+"/:id/lose", core.OpClose, opportunities.Close(m.b, opportunities.CmdLose))     // loseOpportunity
	add("POST", o+"/:id/reopen", core.OpClose, opportunities.Close(m.b, opportunities.CmdReopen)) // reopenOpportunity
	add("GET", "/api/opportunity/funnel", core.OpRead, h(forecast.Funnel))                        // getFunnel
	return nil
}

// Subscriptions declares erp/sales' documents and refusals (OP08): subscribed, not a dependency.
func (m *Module) Subscriptions(s *events.Subscriptions) error {
	s.Add(events.Sub{Name: "sales-documents", Streams: []string{core.SalesStream},
		Types:  []string{core.QuotationChanged, core.OrderChanged, core.QuoteRequestRejected},
		Handle: func(ctx context.Context, tx db.Tx, e events.Received) error { return quotes.OnSales(m.b, ctx, tx, e) }})
	return nil
}
