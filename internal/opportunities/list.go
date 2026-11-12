package opportunities

import (
	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/list"
)

// The opportunity list (protocol 14): newest first, by cursor; the data scope compiled into the query (security 07).
var opportunityList = list.Spec{Operation: core.OpRead, Alias: "o", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "created_at", Column: "created_at", Kind: list.Time, Sortable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "customer_id", Column: "customer_id", Kind: list.UUID, Filterable: true},
		{Field: "stage", Column: "stage", Kind: list.Text, Filterable: true},
		{Field: "owner_id", Column: "owner_id", Kind: list.Text, Filterable: true},
		{Field: "expected_close_date", Column: "expected_close_date", Kind: list.Date, Filterable: true}},
	Search: []list.SearchColumn{{Name: "number_name", Column: "search", Fields: []string{"number", "name"}}}}

// ListOpportunities is one page of the opportunities the caller may see.
func ListOpportunities(m *core.Base, c *httpx.Context) error {
	if err := core.QueryFilters(c, []string{"legal_entity_id", "customer_id", "owner_id"}, []string{"close_from", "close_to"}); err != nil {
		return err
	}
	var f []list.Filter
	for _, k := range []string{"legal_entity_id", "customer_id", "stage", "owner_id"} {
		if v := c.Query(k); v != "" {
			f = append(f, list.Filter{Field: k, Op: "eq", Values: []string{v}})
		}
	}
	if v := c.Query("close_from"); v != "" {
		f = append(f, list.Filter{Field: "expected_close_date", Op: "gte", Values: []string{v}})
	}
	if v := c.Query("close_to"); v != "" {
		f = append(f, list.Filter{Field: "expected_close_date", Op: "lte", Values: []string{v}})
	}
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, opportunityList, list.Request{Filters: f, Q: c.Query("q"), Limit: limit,
		Cursor: c.Query("cursor"), Sort: []list.Order{{Field: "created_at", Desc: true}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "opportunity", Cols, Scan,
		func(o Opportunity) ([]string, string) { return []string{o.CreatedAt}, o.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"opportunities": out, "next_cursor": next})
}
