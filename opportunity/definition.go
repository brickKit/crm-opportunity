// Package opportunity is the opportunity component: opportunities from first contact to won or lost, their stages
// and forecast (the funnel), quote requests to erp/sales and what its quotations and orders say back; CRM and ERP
// only meet by events (crm.md, A71).
package opportunity

import (
	"embed"
	"io/fs"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity.
const (
	Version  = "3.0.8"
	GRPCPort = 9102
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Keys are the component's own configuration keys.
func Keys() []config.Key {
	return []config.Key{
		// when a sales document wins its opportunity (OP08)
		{Name: core.KeyAutoWin, Type: config.Enum, Values: []string{core.AutoManual, core.AutoQuotation, core.AutoOrder}},
	}
}

// Own describes the own keys for the manifest's configSchema.
var Own = map[string]string{
	core.KeyAutoWin: "when erp/sales' documents win their opportunity: manual (never), quotation_accepted or " +
		"order_confirmed; unset: manual",
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: core.ID, Version: Version, Keys: Keys(), Migrations: migrations(), GRPCPort: GRPCPort, Subscribes: true,
		Streams: []events.Stream{{Name: core.Stream, Partitions: 4}}, Authz: core.Definitions(), Resend: core.OpResend,
		Recovery: core.OpRecover, New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
