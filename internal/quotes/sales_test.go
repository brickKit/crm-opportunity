package quotes

import (
	"testing"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/stretchr/testify/assert"
)

// TestAutoWins: OP08's setting, every kind and state that matters (the integration test runs order_confirmed only:
// the setting is the process's).
func TestAutoWins(t *testing.T) {
	for _, c := range []struct {
		setting, kind, state string
		wins                 bool
	}{
		{core.AutoManual, KindQuotation, "accepted", false},
		{core.AutoManual, KindOrder, "confirmed", false},
		{core.AutoQuotation, KindQuotation, "accepted", true},
		{core.AutoQuotation, KindQuotation, "sent", false},
		{core.AutoQuotation, KindOrder, "confirmed", false},
		{core.AutoOrder, KindOrder, "confirmed", true},
		{core.AutoOrder, KindOrder, "draft", false},
		{core.AutoOrder, KindOrder, "cancelled", false},
		{core.AutoOrder, KindQuotation, "accepted", false},
	} {
		assert.Equal(t, c.wins, AutoWins(c.setting, c.kind, c.state), "%v", c)
	}
}
