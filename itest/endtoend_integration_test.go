//go:build integration

package itest

// The opportunity component against PostgreSQL and the bus over its real surfaces (L2). The master data components
// are stand-ins that speak their published contracts over mTLS (standins_integration_test.go); erp/sales only meets
// this component by events, which the test publishes on its stream as erp/sales' forwarder would. Needs
// BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL, as be-sdk-go's integration tests. The member listens on 28102 (user)
// and 9102 (system); the stand-ins on 19150-19152. The automatic win is set to order_confirmed.
// Run: go test -tags integration ./itest/ (make test-integration); the index of the steps is itest/AGENTS.md.
//
// Each step is in the file of its business flow; TestOpportunityEndToEnd runs them in order.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"
	opportunity "github.com/brickKit/crm-opportunity/v3/opportunity"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/events/bus"
	"github.com/brickKit/be-sdk-go/v3/events/jetstream"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	itPort     = "28102"
	itBase     = "http://localhost:" + itPort
	itSystem   = "localhost:9102"
	itOrg      = "localhost:19150"
	itPartner  = "localhost:19151"
	itCurrency = "localhost:19152"
	itSchema   = "crm_opportunity_it"
)

// fixture is the running member, its stand-ins and the world's IDs, shared by the steps.
type fixture struct {
	t     *testing.T
	ctx   context.Context
	w     *sectest.World
	admin *pgx.Conn
	alice client // every operation everywhere
	bob   client // a sales manager: every operation everywhere, assigns
	dave  client // a salesperson: reads, edits and closes the opportunities he owns, does not assign
	erin  client // edits every opportunity but does not assign
	org   *orgStandIn
	tr    events.Transport
	rt    *runtime.Runtime // the member's runtime

	// Shanghai (SH, CNY) and Beijing (BJ, CNY); a customer, one with no customer role in Beijing, a suspended one
	le, le2, customer, shOnly, blocked string
	// what one step leaves for the next: opportunities by name
	opps map[string]map[string]any
}

// erin is a fifth principal of the test deployment: an assistant who edits but does not assign.
const erin = "0190f3a1-0000-7000-8000-0000000000e5"

// dave is a fourth principal of the test deployment: a salesperson.
const dave = "0190f3a1-0000-7000-8000-0000000000d4"

var salesStream = events.Stream{Name: core.SalesStream, Partitions: 4}

func TestOpportunityEndToEnd(t *testing.T) {
	fx := start(t)
	fx.opportunities()
	fx.quotes()
	fx.funnel()
	fx.outbox()
	fx.recoveryChecks()
}

// start makes the world, the stand-ins and erp/sales' stream, then migrates and runs the member.
func start(t *testing.T) *fixture {
	adminURL, natsURL := os.Getenv("BE_SDK_TEST_PG_ADMIN"), os.Getenv("BE_SDK_TEST_NATS_URL")
	if adminURL == "" || natsURL == "" {
		t.Fatal("BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL are needed")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close(ctx) })
	_, err = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+itSchema+" CASCADE; CREATE SCHEMA "+itSchema)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	w := sectest.New(t)
	w.GrantAll(sectest.Alice, core.Definitions())
	w.GrantAll(sectest.Bob, core.Definitions())
	w.Grant(dave, own(core.Definitions(), core.OpRead, core.OpEdit, core.OpClose)...)
	w.Grant(erin, everyone(core.Definitions(), core.OpRead, core.OpEdit)...)
	fx := &fixture{t: t, ctx: ctx, w: w, admin: admin, le: ids.New().String(), le2: ids.New().String(),
		customer: ids.New().String(), shOnly: ids.New().String(), blocked: ids.New().String(), opps: map[string]map[string]any{}}
	fx.org = &orgStandIn{les: map[string]map[string]any{fx.le: {"id": fx.le, "code": "SH", "state": "active",
		"timezone": "Asia/Shanghai", "base_currency": "CNY", "version": int64(1)},
		fx.le2: {"id": fx.le2, "code": "BJ", "state": "active", "timezone": "Asia/Shanghai", "base_currency": "CNY",
			"version": int64(1)}}, units: map[string]map[string]any{}}
	portFree(t, itPort, itSystem, itOrg, itPartner, itCurrency)
	serve(t, itOrg, w, core.Org, func(s *grpc.Server) { orgv3.RegisterSourceServiceServer(s, fx.org) })
	serve(t, itPartner, w, core.Partner, func(s *grpc.Server) {
		p := &partnerStandIn{names: map[string]string{fx.customer: "上海客户", fx.shOnly: "只在上海的客户", fx.blocked: "暂停的客户"},
			blocked: map[string]bool{fx.blocked: true}, noRole: map[string]string{fx.shOnly: fx.le2}}
		partnerv3.RegisterSourceServiceServer(s, p)
		partnerv3.RegisterPartnerServiceServer(s, p)
	})
	serve(t, itCurrency, w, core.Currency, func(s *grpc.Server) { currencyv3.RegisterSourceServiceServer(s, currencyStandIn{}) })

	// a clean stream each run (earlier runs and cells leave events on the shared NATS); erp/sales' stream exists
	// before the subscription starts (its forwarder makes it in a deployment)
	js, err := jetstream.Connect(natsURL, "opportunity-it-clean")
	require.NoError(t, err)
	for _, s := range []string{salesStream.Name, core.Stream} {
		_ = js.DeleteStream(ctx, s) // not there: nothing to clean
	}
	_ = js.Close()
	fx.tr, err = bus.Open(ctx, natsURL, "opportunity-it-sales", slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { fx.tr.Close() })
	require.NoError(t, fx.tr.EnsureStream(ctx, salesStream))

	env := w.Env(t, core.ID)
	for k, v := range map[string]string{"COMPONENT_ID": core.ID, "COMPONENT_VERSION": opportunity.Version, "PORT": itPort,
		"STOP_DRAIN_DELAY": "50ms", "PG_HOST": u.Hostname(), "PG_PORT": u.Port(), "PG_DATABASE": strings.TrimPrefix(u.Path, "/"),
		"PG_USER": u.User.Username(), "PG_PASSWORD": pw, "PG_SCHEMA": itSchema, "PG_OWNER_USER": u.User.Username(),
		"PG_OWNER_PASSWORD": pw, "BUS_URL": natsURL, "IMPLEMENTATION_INSTANCE": ids.New().String(),
		"MDM_ORG_GRPC_ENDPOINT": "http://" + itOrg, "MDM_PARTNER_GRPC_ENDPOINT": "http://" + itPartner,
		"MDM_CURRENCY_GRPC_ENDPOINT": "http://" + itCurrency, core.KeyAutoWin: core.AutoOrder} {
		env[k] = v
	}
	var logs bytes.Buffer
	var logMu sync.Mutex
	t.Cleanup(func() {
		if t.Failed() {
			logMu.Lock()
			t.Log(logs.String())
			logMu.Unlock()
		}
	})
	out := &lockedWriter{w: &logs, mu: &logMu}
	opts := launcher.Options{Stdout: out, Stderr: out,
		Env: config.Env{LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Read: os.ReadFile}}
	migrate := opts
	migrate.Args = []string{"migrate"}
	def := opportunity.Definition()
	newModule := def.New
	def.New = func(rt *runtime.Runtime) (component.Module, error) { fx.rt = rt; return newModule(rt) }
	require.Equal(t, launcher.ExitOK, launcher.Run(ctx, def, migrate))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- launcher.Run(sctx, def, opts) }()
	t.Cleanup(func() { stop(); <-done })
	require.Eventually(t, func() bool {
		r, err := http.Get(itBase + "/readyz")
		if err == nil {
			r.Body.Close()
		}
		return err == nil && r.StatusCode == 200
	}, 20*time.Second, 100*time.Millisecond)
	fx.alice = client{t, w.Bearer(sectest.Alice)}
	fx.bob = client{t, w.Bearer(sectest.Bob)}
	fx.dave = client{t, w.Bearer(dave)}
	fx.erin = client{t, w.Bearer(erin)}
	return fx
}

// everyone grants the operations on every opportunity, with every field.
func everyone(defs *sdkauthz.Definitions, ops ...string) []sdkauthz.Segment {
	segs := own(defs, ops...)
	for i := range segs {
		segs[i].Atoms[core.DimOwner] = sdkauthz.Atom{Kind: "all"}
	}
	return segs
}

// own grants the operations in every legal entity on the opportunities the grantee owns (the owner dimension's
// self), with every field.
func own(defs *sdkauthz.Definitions, ops ...string) []sdkauthz.Segment {
	var out []sdkauthz.Segment
	for _, op := range ops {
		var fields []string
		for f := range defs.Resources[0].Fields {
			fields = append(fields, f)
		}
		out = append(out, sdkauthz.Segment{ResourceType: core.Opportunities, Operation: op,
			Atoms:      map[string]sdkauthz.Atom{core.DimLegalEntity: {Kind: "all"}, core.DimOwner: {Kind: "self"}},
			ReadFields: fields, Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}})
	}
	return out
}

// fact reads the payload of an event in the outbox whose key has the value; nil when there is none.
func (fx *fixture) outboxed(typ, key, value string) map[string]any {
	fx.t.Helper()
	var payload []byte
	err := fx.admin.QueryRow(fx.ctx, "SELECT payload FROM "+itSchema+".be_outbox WHERE type = $1 AND "+
		"convert_from(payload, 'UTF8')::jsonb->>'"+key+"' = $2 ORDER BY ord DESC LIMIT 1", typ, value).Scan(&payload)
	if err == pgx.ErrNoRows {
		return nil
	}
	require.NoError(fx.t, err)
	var out map[string]any
	require.NoError(fx.t, json.Unmarshal(payload, &out))
	return out
}

// --- helpers ---

type client struct {
	t     *testing.T
	token string
}

// do calls the user surface; a POST carries a new Idempotency-Key unless key gives one.
func (c client) do(method, path string, body any, key ...string) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, itBase+path, r)
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" {
		k := ids.New().String()
		if len(key) > 0 {
			k = key[0]
		}
		req.Header.Set("Idempotency-Key", k)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func serve(t *testing.T, addr string, w *sectest.World, as string, register func(*grpc.Server)) {
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(w.TLS(t, as).ServerConfig())))
	register(srv)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
}

// at reads a value along a path of keys and indexes from a decoded JSON object.
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func (fx *fixture) get(path string) map[string]any {
	fx.t.Helper()
	code, out := fx.alice.do("GET", path, nil)
	require.Equal(fx.t, 200, code, out)
	return out
}

// salesEpoch is the epoch erp/sales' events carry (the test plays erp/sales' forwarder).
var salesEpoch, _ = events.ParseEpoch("1.1")

// publishSales publishes an event on erp/sales' stream as its forwarder would, each time under a new event ID: a full
// state by its version (a quotation, an order) or an incremental fact (version 1).
func (fx *fixture) publishSales(typ, id string, version int64, data map[string]any) {
	fx.t.Helper()
	b, _ := json.Marshal(data)
	row := events.Row{EventID: ids.New().String(), Stream: salesStream.Name, Partition: salesStream.PartitionOf(id),
		Type: typ, OccurredAt: time.Now(), RecordID: id, Version: version, Projection: events.Full, Payload: b,
		Seq: time.Now().UnixNano(), Epoch: salesEpoch}
	row.Digest = events.Digest(row.EventID, b)
	require.NoError(fx.t, fx.tr.Publish(fx.ctx, events.Message{Subject: events.Subject(salesStream.Name, row.Partition, row.Type),
		Headers: row.Headers(core.Sales, "sales-it"), Data: b}))
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// portFree fails at once when a port the member needs is taken: a running cell or another member would answer in
// its place, and the test would assert against another process.
func portFree(t *testing.T, addrs ...string) {
	for _, a := range addrs {
		if !strings.Contains(a, ":") {
			a = "localhost:" + a
		}
		ln, err := net.Listen("tcp", a)
		require.NoError(t, err, fmt.Sprintf("port %s is taken: is a cell or another member running?", a))
		_ = ln.Close()
	}
}
