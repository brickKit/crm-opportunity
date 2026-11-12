// Command seed fills a running crm/opportunity with the seed data for people (be-assembly-standard's seed and test
// data conventions):
//
//   - access, by the authorization admin: the role maintain (the seed user: every operation, every legal entity and
//     owner); the salespeople's own opportunities (test.sales.cn and test.sales.multi in CN01, test.sales.es in ES01:
//     read, edit, win and lose what they own); a sales manager (test.unit.admin.cn, the head of CN01's sales: every
//     owner in CN01, and assigning);
//   - opportunities: CN01 220 (more than a page), ES01 40, US01 30, owned in turn by the salespeople of the legal
//     entity and the seed user, for customers with an active customer role there (in the role's currency), with one
//     to three products sellable there; in every stage (qualification, needs analysis, proposal, negotiation, won with
//     its won amount, lost with a reason), one lost and reopened in each legal entity;
//   - quote requests: up to ten opportunities in proposal per legal entity ask erp/sales for a quotation (erp/sales,
//     when deployed, makes the drafts: they show in its quotations, owned by the opportunity's owner).
//
// Opportunities are seeded once, into a legal entity that has none yet; each request has its own idempotency key, so
// a run that broke off is finished by running it again within a day. The legal entities, customers and products come
// from mdm/org, mdm/partner and mdm/product (their seeds run first).
//
//	SEED_IAM_URL (http://localhost:18200)    the identity member's development exchange
//	SEED_AUTHZ_URL (http://localhost:18223)  the authorization member's effect point
//	SEED_ADMIN (test.alice)                  the authorization admin (the install claim)
//	SEED_USER (test.bob), SEED_PASSWORD      the user that writes the data; the test accounts' password
//	CRM_OPPORTUNITY_URL (http://localhost:18102), MDM_ORG_URL (http://localhost:18083),
//	MDM_PARTNER_URL (http://localhost:18225), MDM_PRODUCT_URL (http://localhost:18082)
package main

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/seed"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed crm/opportunity: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

type client struct{ seed.Client }

func (c client) must(method, path string, body any, header ...string) map[string]any {
	out, err := c.Must(method, path, body, header...)
	check(err)
	return out
}

// pages reads every page of a list.
func (c client) pages(path, key string) []map[string]any {
	var out []map[string]any
	cursor := ""
	for {
		page := c.must("GET", path+"&limit=200&cursor="+url.QueryEscape(cursor), nil)
		for _, x := range page[key].([]any) {
			out = append(out, x.(map[string]any))
		}
		if cursor, _ = page["next_cursor"].(string); cursor == "" {
			return out
		}
	}
}

// segment grants an operation on opportunities in the legal entities (all when none), the owner self or any.
func segment(op string, legalEntities []string, self bool) sdkauthz.Segment {
	s := seed.Segment("crm.opportunity", op)
	le := sdkauthz.Atom{Kind: "all"}
	if len(legalEntities) > 0 {
		le = sdkauthz.Atom{Kind: "in", Values: legalEntities}
	}
	owner := sdkauthz.Atom{Kind: "all"}
	if self {
		owner = sdkauthz.Atom{Kind: "self"}
	}
	s.Atoms = map[string]sdkauthz.Atom{"legal_entity": le, "owner": owner}
	return s
}

func segments(les []string, self bool, ops ...string) []sdkauthz.Segment {
	var out []sdkauthz.Segment
	for _, op := range ops {
		out = append(out, segment(op, les, self))
	}
	return out
}

type customer struct{ id, currency string }

type product struct{ id, unit string }

type world struct {
	les       map[string]string     // code → ID
	customers map[string][]customer // legal entity code → active customer roles
	products  map[string][]product  // legal entity code → sellable products
}

// The stages in turn: three in ten qualification, two needs analysis, two proposal, one negotiation, one won, one lost.
var stages = []string{"qualification", "qualification", "qualification", "needs_analysis", "needs_analysis", "proposal",
	"proposal", "negotiation", "won", "lost"}

var lostReasons = []string{"价格高于竞争对手", "客户预算取消", "交期不满足", "选择了现有供应商"}

func main() {
	base := env("CRM_OPPORTUNITY_URL", "http://localhost:18102")
	if !seed.Ready(base) {
		fail("crm/opportunity at %s is not ready", base)
	}
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test accounts' password in Casdoor)")
	}
	iam, user, admin := env("SEED_IAM_URL", "http://localhost:18200"), env("SEED_USER", "test.bob"), env("SEED_ADMIN", "test.alice")
	accounts, err := seed.Accounts(iam, pw, admin, user, "test.sales.cn", "test.sales.es", "test.sales.multi", "test.unit.admin.cn")
	check(err)
	t := accounts[user].Token
	w := read(t)
	a, err := seed.NewAuthz(env("SEED_AUTHZ_URL", "http://localhost:18223"), accounts[admin].Token, "crm/opportunity")
	check(err)
	all := []string{"crm.opportunity.read", "crm.opportunity.edit", "crm.opportunity.close", "crm.opportunity.assign"}
	own := []string{"crm.opportunity.read", "crm.opportunity.edit", "crm.opportunity.close"}
	cn, es := []string{w.les["CN01"]}, []string{w.les["ES01"]}
	_, err = a.GrantRoles([]seed.RoleSpec{
		{Key: "maintain", Name: prefix + "商机维护", To: []string{user}, Segments: segments(nil, false, all...)},
		{Key: "own-cn", Name: prefix + "商机（CN01 自己的）", To: []string{"test.sales.cn", "test.sales.multi"},
			Segments: segments(cn, true, own...)},
		{Key: "own-es", Name: prefix + "商机（ES01 自己的）", To: []string{"test.sales.es"}, Segments: segments(es, true, own...)},
		{Key: "manager-cn", Name: prefix + "销售主管（CN01 全部商机）", To: []string{"test.unit.admin.cn"},
			Segments: segments(cn, false, all...)},
	}, accounts)
	check(err)

	c := client{seed.NewClient(base, t)}
	owners := map[string][]string{
		"CN01": {accounts["test.sales.cn"].Principal, accounts["test.sales.multi"].Principal, accounts[user].Principal},
		"ES01": {accounts["test.sales.es"].Principal, accounts[user].Principal},
		"US01": {accounts[user].Principal}}
	for _, le := range []struct {
		code string
		n    int
	}{{"CN01", 220}, {"ES01", 40}, {"US01", 30}} {
		if len(c.must("GET", "/api/opportunity/opportunities?limit=1&legal_entity_id="+w.les[le.code], nil)["opportunities"].([]any)) > 0 {
			fmt.Printf("seed crm/opportunity: %s has opportunities already; they are not seeded again\n", le.code)
			continue
		}
		made, requests := opportunities(c, w, le.code, le.n, owners[le.code])
		fmt.Printf("seed crm/opportunity: %s: %d opportunities, %d quote requests\n", le.code, made, requests)
	}
}

// read gathers what the seed needs from the components it builds on.
func read(t string) world {
	w := world{les: map[string]string{}, customers: map[string][]customer{}, products: map[string][]product{}}
	org := client{seed.NewClient(env("MDM_ORG_URL", "http://localhost:18083"), t)}
	for _, e := range org.must("GET", "/api/org/legal-entities?limit=200", nil)["legal_entities"].([]any) {
		e := e.(map[string]any)
		w.les[e["code"].(string)] = e["id"].(string)
	}
	code := map[string]string{}
	for _, le := range []string{"CN01", "ES01", "US01"} {
		if w.les[le] == "" {
			fail("mdm/org has no legal entity %s: run its seed first", le)
		}
		code[w.les[le]] = le
	}
	partner := client{seed.NewClient(env("MDM_PARTNER_URL", "http://localhost:18225"), t)}
	partners := partner.pages("/api/partner/partners?state=active", "partners")
	sort.Slice(partners, func(i, j int) bool { return partners[i]["name"].(string) < partners[j]["name"].(string) })
	for _, p := range partners {
		for _, r := range partner.must("GET", "/api/partner/partners/"+p["id"].(string)+"/roles", nil)["roles"].([]any) {
			r := r.(map[string]any)
			le, _ := r["legal_entity_id"].(string)
			cur, _ := r["default_currency"].(string)
			if r["role"] == "customer" && r["state"] == "active" && code[le] != "" && cur != "" {
				w.customers[code[le]] = append(w.customers[code[le]], customer{p["id"].(string), cur})
			}
		}
	}
	mdmProduct := client{seed.NewClient(env("MDM_PRODUCT_URL", "http://localhost:18082"), t)}
	products := mdmProduct.pages("/api/product/products?state=active", "products")
	sort.Slice(products, func(i, j int) bool { return products[i]["code"].(string) < products[j]["code"].(string) })
	for _, p := range products {
		for _, av := range p["availability"].([]any) {
			av := av.(map[string]any)
			if le := code[av["legal_entity_id"].(string)]; le != "" && av["sellable"] == true {
				w.products[le] = append(w.products[le], product{p["id"].(string), p["base_unit"].(string)})
			}
		}
	}
	for _, le := range []string{"CN01", "ES01", "US01"} {
		if len(w.customers[le]) == 0 || len(w.products[le]) == 0 {
			fail("%s has no customer or no sellable product: run mdm/partner's and mdm/product's seeds first", le)
		}
	}
	return w
}

// opportunities makes n opportunities in a legal entity and walks each to its stage; it answers how many it made and
// how many quote requests it sent.
func opportunities(c client, w world, le string, n int, owners []string) (int, int) {
	key := func(what string, i int) string { return fmt.Sprintf("seed:crm/opportunity:%s:%s:%d", le, what, i) }
	post := func(path, k string, body any) map[string]any {
		return c.must("POST", path, body, "Idempotency-Key", k)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	requests := 0
	for i := 0; i < n; i++ {
		cust := w.customers[le][i%len(w.customers[le])]
		var lines []any
		for k := 0; k <= i%3; k++ {
			p := w.products[le][(i*3+k)%len(w.products[le])]
			lines = append(lines, map[string]any{"product_id": p.id, "quantity": fmt.Sprint(1 + (i+k)%9), "unit": p.unit})
		}
		stage := stages[i%len(stages)]
		open := stage
		if stage == "won" || stage == "lost" {
			open = "negotiation"
		}
		o := post("/api/opportunity/opportunities", key("opportunity", i), map[string]any{
			"legal_entity_id": w.les[le], "customer_id": cust.id, "currency": cust.currency, "owner_id": owners[i%len(owners)],
			"name": fmt.Sprintf("%s 商机 %03d", le, i+1), "amount": fmt.Sprint(1000 * (1 + i%37)), "stage": open,
			"expected_close_date": start.AddDate(0, i%6, i%28).Format("2006-01-02"), "lines": lines})
		path := "/api/opportunity/opportunities/" + o["id"].(string)
		switch {
		case stage == "won" && o["stage"] == "negotiation":
			post(path+"/win", key("win", i), map[string]any{"won_amount": fmt.Sprint(900 * (1 + i%37))})
		case stage == "lost" && o["stage"] == "negotiation":
			post(path+"/lose", key("lose", i), map[string]any{"reason": lostReasons[i%len(lostReasons)]})
		}
		if i == 9 { // the first lost one, reopened (back to negotiation)
			cur := c.must("GET", path, nil)
			if cur["stage"] == "lost" {
				post(path+"/reopen", key("reopen", i), map[string]any{"reason": "客户重新询价"})
			}
		}
		if stage == "proposal" && requests < 10 && i%4 == 1 {
			post(path+"/quote-requests", key("quote", i), nil)
			requests++
		}
	}
	return n, requests
}
