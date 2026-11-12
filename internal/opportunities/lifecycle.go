package opportunities

import (
	"regexp"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
)

// An opportunity's lifecycle in one table (docs/design.md, "商机"; OP02-OP08): its phases are open (the four stages
// in progress, alike in the table; how they move among themselves is OP03), won and lost. Creating is not in the
// table (OP01: a new opportunity is open).

const (
	PhaseOpen = "open"
	PhaseWon  = "won"
	PhaseLost = "lost"
)

// The commands.
const (
	CmdEdit         = "edit"
	CmdAdvance      = "advance"
	CmdRevert       = "revert"
	CmdAssign       = "assign"
	CmdRequestQuote = "request_quote"
	CmdWin          = "win"
	CmdLose         = "lose"
	CmdReopen       = "reopen"
	CmdSalesEvent   = "sales_event"
)

// PhaseOf is the phase of a stage.
func PhaseOf(stage string) string {
	switch stage {
	case core.StageWon:
		return PhaseWon
	case core.StageLost:
		return PhaseLost
	}
	return PhaseOpen
}

// Lifecycle is the table.
var Lifecycle = core.Table{
	Rule:     regexp.MustCompile(`^OP\d\d$`),
	Phases:   []string{PhaseOpen, PhaseWon, PhaseLost},
	Commands: []string{CmdEdit, CmdAdvance, CmdRevert, CmdAssign, CmdRequestQuote, CmdWin, CmdLose, CmdReopen, CmdSalesEvent},
	Cells: map[string]map[string]core.Decision{
		CmdEdit: {
			PhaseOpen: core.ProceedCell("OP02"),
			PhaseWon:  core.ProceedCell("OP02"), // only the name and the expected close date (the guard in edit)
			PhaseLost: core.RefuseCell("OP02", "a lost opportunity changes after it is reopened"),
		},
		CmdAdvance: {
			PhaseOpen: core.ProceedCell("OP03"),
			PhaseWon:  core.RefuseCell("OP03", "a {state} opportunity has no stage to advance to; reopen it first"),
			PhaseLost: core.RefuseCell("OP03", "a {state} opportunity has no stage to advance to; reopen it first"),
		},
		CmdRevert: {
			PhaseOpen: core.ProceedCell("OP03"),
			PhaseWon:  core.RefuseCell("OP03", "a {state} opportunity is reopened, not reverted"),
			PhaseLost: core.RefuseCell("OP03", "a {state} opportunity is reopened, not reverted"),
		},
		CmdAssign: {
			PhaseOpen: core.ProceedCell("OP04"),
			PhaseWon:  core.ProceedCell("OP04"),
			PhaseLost: core.ProceedCell("OP04"),
		},
		CmdRequestQuote: {
			PhaseOpen: core.ProceedCell("OP05"),
			PhaseWon:  core.RefuseCell("OP05", "a {state} opportunity asks for no quotation"),
			PhaseLost: core.RefuseCell("OP05", "a {state} opportunity asks for no quotation"),
		},
		CmdWin: {
			PhaseOpen: core.MoveCell("OP06", PhaseWon, core.OpClose),
			PhaseWon:  core.IgnoreCell("OP06"),
			PhaseLost: core.RefuseCell("OP06", "a lost opportunity is reopened before it is won"),
		},
		CmdLose: {
			PhaseOpen: core.MoveCell("OP06", PhaseLost, core.OpClose),
			PhaseWon:  core.RefuseCell("OP06", "a won opportunity is reopened before it is lost"),
			PhaseLost: core.IgnoreCell("OP06"),
		},
		CmdReopen: {
			PhaseOpen: core.IgnoreCell("OP07"),
			PhaseWon:  core.MoveCell("OP07", PhaseOpen, core.OpClose),
			PhaseLost: core.MoveCell("OP07", PhaseOpen, core.OpClose),
		},
		CmdSalesEvent: {
			PhaseOpen: core.ProceedCell("OP08"),
			PhaseWon:  core.ProceedCell("OP08"),
			PhaseLost: core.ProceedCell("OP08"),
		},
	},
}
