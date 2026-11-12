package core

import (
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Base is what every module works with: the component's runtime (its database, calls, events, audit, logger) and
// the configuration read once at the start.
type Base struct {
	RT *runtime.Runtime
	// AutoWin is the deployment's setting of the automatic win (KeyAutoWin, OP08): AutoManual, AutoQuotation or
	// AutoOrder.
	AutoWin string
}

// KeyAutoWin is the component's one configuration key (opportunity/definition.go declares it).
const KeyAutoWin = "OPPORTUNITY_AUTO_WIN"

// The values of KeyAutoWin.
const (
	AutoManual    = "manual"
	AutoQuotation = "quotation_accepted"
	AutoOrder     = "order_confirmed"
)

// NewBase reads the configuration a module needs (the SDK checked the value against the enum).
func NewBase(rt *runtime.Runtime) (*Base, error) {
	b := &Base{RT: rt, AutoWin: AutoManual}
	if rt.Config.Has(KeyAutoWin) {
		b.AutoWin = rt.Config.String(KeyAutoWin)
	}
	return b, nil
}
