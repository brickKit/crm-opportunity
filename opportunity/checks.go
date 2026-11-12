package opportunity

import (
	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/recovery"
)

// RecoveryChecks registers what a full recovery asks this member (be-protocol chapter 14; docs/design.md, "整套恢复
// 之后"): whether the opportunities its audit names are still there and at which version. Its numbers are a gapped
// series: nothing to void.
func (m *Module) RecoveryChecks(c *recovery.Checks) error {
	c.Presence(core.Opportunities, recovery.TablePresence("opportunity", "id", "version"))
	return nil
}
