[English](AGENTS.md) · [中文](AGENTS.zh.md)

# crm/opportunity

The AI guide to developing this component. How to use it, its boundaries and contracts: BRICKKIT.md. Why it is shaped this way: `docs/design.md`. Dependencies, configuration and deployment: component.yaml.

## Code map

| Path | Owns |
|---|---|
| `backend/module/module.go` | The only entry, `New(ctx, rt)`: takes the database identity from `PG_USER` / `PG_SCHEMA` (`dbIdentity`), builds repo → service → HTTP + gRPC; `Start` runs the outbox pump, the partition loop and the event consumers side by side. Same function standalone and in a shell |
| `backend/cmd/server/main.go` | One line, `besdk.RunStandalone(module.New)` |
| `backend/cmd/migrate/main.go` | One line, `migrate.Main(migrations.FS)`: the migration container's entry (`./migrate up`) |
| `backend/internal/repo/repo.go` | Sentinel errors (`ErrInvalidArgument`, `ErrNotFound`, `ErrOpportunityTerminal`, `ErrForbidden`), `InScope`, idempotency helpers |
| `backend/internal/repo/write.go` | The five write commands on one transaction skeleton, `runCommand`: claim the idempotency key → change → outbox, all in one transaction |
| `backend/internal/repo/opportunity.go` | The `Opportunity` type, single read, `BatchGetOpportunities`, `ListOpportunities` (scope switch, keyset paging) |
| `backend/internal/repo/decimal.go` | Input validation (`ValidateAmount`, `ValidateQty`, `ValidateProbability`) and exact decimal arithmetic for subtotals and weighted amounts |
| `backend/internal/repo/funnel.go` | The per-stage funnel, summed in SQL |
| `backend/internal/repo/history.go` | Stage history reads |
| `backend/internal/repo/order.go` | `BackfillOrderIDTx`, the `order_id` backfill run inside the consumer's transaction |
| `backend/internal/repo/stage.go` | Stage definitions (read only) |
| `backend/internal/repo/snapshot.go` | The customer-name copy kept from `mdm.customer.*` events |
| `backend/internal/repo/cursor.go` | The list cursor: `created_at` and `id` |
| `backend/internal/service/` | Validation before any network call, the master-data checks, data-scope checks, log levels by error kind, and the error → gRPC status mapping (`status.go`) |
| `backend/internal/client/` | `Deps`: the two gRPC calls to master data, each with its own timeout |
| `backend/internal/consumer/` | Consumers of `mdm.customer.*` and `sales.order.created.v1`; subjects are a struct so tests can use private ones |
| `backend/internal/http/http.go` | REST routes, each registered with its permission key |
| `backend/internal/grpc/grpc.go` | `crm.opportunity.v1.OpportunityService` |
| `backend/internal/partition/` | Background loop creating weekly partitions of `event_outbox` / `event_inbox` four weeks ahead |
| `migrations/` | SQL migrations (tables, stage seeds, `order_id`, history probability), embedded by `migrations/embed.go` |
| `contracts/` | proto, OpenAPI, event schema |
| `gen/crm/opportunity/` | Generated Go code: a nested Go module with its own tags; never edited by hand |
| `backend/testdb/` | Test only: the schema and role the database tests use (`TEST_PG_SCHEMA` / `TEST_PG_ROLE`, default the identity `make test-db-init` creates) and `Q` for `{schema}` in raw SQL; production code must not import it (`make module-check`) |
| `scripts/` | `seed.sh` / `seed-clean.sh` for local demo data; `schema.sh` reads each component's `PG_SCHEMA` from the assembly project's configuration files |

| Feature | Start here | Then |
|---|---|---|
| A new rule on a write command | `backend/internal/repo/write.go` | `backend/internal/service/service.go` (checks before network calls), `backend/internal/repo/write_test.go` |
| A new numeric input | `backend/internal/repo/decimal.go` (a strict regular expression) | `backend/internal/repo/validate_test.go` |
| A new REST route | `backend/internal/http/http.go` (`besdk.GET` / `POST` / `PATCH` with a key from `assembly.yaml`) | `contracts/opportunity.openapi.yaml`; `backend/internal/http/routes_test.go` for a static path next to `:id` |
| A dashboard figure | `backend/internal/repo/funnel.go` | `backend/internal/service/funnel_test.go` (two users, two departments) |
| Reacting to another component's event | `backend/internal/consumer/consumer.go` | `backend/internal/repo/order.go`, `backend/internal/consumer/order_test.go` |
| A new field on the opportunity | `migrations/` (a new migration) | `contracts/` (append only), `buf generate`, `backend/internal/repo/opportunity.go` |
| An error answering with the wrong status | `backend/internal/service/status.go` | the sentinel errors in `backend/internal/repo/repo.go` |

## Build and test

```bash
# tests run against the test database brickkit_test_db, never brickkit_db (make test-db-init ID=crm/opportunity at the project root)
# the password is POSTGRES_PASSWORD in the project's .env (URL-encode it); don't source .env (Compose syntax, multi-line PEM)
export TEST_PG_DSN="postgres://postgres:<password>@localhost:5432/brickkit_test_db?sslmode=disable"
make test                    # every package ends in "ok"; refuses to run without TEST_PG_DSN
go test ./... -count=1 -v | grep -- '--- SKIP'   # only the three cross-component tests in backend/internal/service/ (service_test.go, nodept_test.go)
make check-version dag-check contract-check import-scan module-check   # each prints one ✓ line
make docs-check              # "0 with errors, 0 warnings"
```

The three skipped tests need `mdm/customer` and `mdm/product` running; from the project root, `make test-cross ID=crm/opportunity` points them at the running containers, and there they must pass. Migrations: `make test-db-init ID=crm/opportunity` at the project root runs them twice as the role the project registers for this component (`✓ 迁移幂等`). The whole suite also runs against another identity: migrate a schema as its own login role, then `TEST_PG_SCHEMA=<schema> TEST_PG_ROLE=<role> make test`; `backend/module/identity_test.go` does this by itself with throwaway names. After `buf generate` (contracts changed), the contract package needs a new tag: see Pitfalls. On a real machine, from the project root: `make verify ID=crm/opportunity ROUTE=/crm/opportunity/opportunities FOCUS=1` builds the image, starts only what this component needs, checks migration, health and the permission check, runs a focus run, and tears down.

## Design decisions

- **CRM and ERP exchange only events.** Winning publishes `crm.opportunity.won.v1`; the order ID returns through `sales.order.created.v1`. Nothing here calls `erp/*`.
- **Two synchronous edges, both to master data,** used only when an opportunity is created, through `besdk.UserClient` (see BRICKKIT on what it forwards), so bad customers and products are blocked before anything is written.
- **Every write is idempotent and commits together with its event** (`runCommand`): a committed change always publishes, a rolled-back one never does.
- **Numbers are validated with regular expressions, not by the database:** `NUMERIC` stores `NaN` and rounds extra decimals silently.
- **The weighted amount is computed, never stored**; all money arithmetic is decimal.
- **Two data-scope dimensions, `org` and `owner`, the same as `erp/sales`.** Lists and the funnel switch between them with `view`; single reads accept either and answer `404` outside both, writes answer `403`.
- **A caller without a department matches no `org` row.** The SDK's prefix for an empty `dept_path` matches nothing; such a caller still creates opportunities, with an empty department snapshot, visible only to the owner.
- **`order_id` is written once.** A second order claiming the same opportunity is logged at `ERROR`, not applied.

## Pitfalls

| Never | Symptom | Why |
|---|---|---|
| Add `erp/sales` (or any `erp/*`, `infra/workflow`) to `dependencies.components`, or call it from `MarkWon` | Winning fails when stock is short although the deal was won; a CRM-only project cannot be assembled | Only events cross between CRM and ERP; `make dag-check` fails on such an edge |
| Validate an amount with `strconv.ParseFloat`, or pass it to SQL unchecked | `NaN` is stored and the funnel sums turn into `NaN`; `1e3` and `100.005` are accepted silently; `abc` answers `500` | Use the validators in `backend/internal/repo/decimal.go`; `backend/internal/repo/validate_test.go` lists the cases |
| Combine `owner_id = me OR dept_path LIKE …` on the list or the funnel | `view=mine` shows the whole department's figures | The two views are a switch chosen by `view`; `backend/internal/service/funnel_test.go` checks two users in two departments |
| Treat an empty `dept_path` or `ScopePrefix` as the root of the tree, or write `scope.Prefix` into a row without checking `HasDept` | A new account with only `crm.opportunity.view` sees and wins every opportunity; or rows carry the SDK's no-department marker as if it were a department | An empty path means "no department", not "everything"; the root is `/<root id>/` or `"/"`. `creatorSnapshot` writes an empty department when `HasDept` is false; `backend/internal/service/nodept_test.go` checks each route |
| Overwrite a non-empty `order_id` in the consumer | The first order silently loses its link to the opportunity | One opportunity converts once; a conflict is a data problem for a person |
| Drop `owner_id` or `dept_path` from the won event | `erp/sales` creates the order, but nobody's list shows it | The order inherits the opportunity's ownership from the event; there is no user token in that handler |
| Call `mdm/customer` / `mdm/product` inside the repo transaction | Under a slow dependency, row locks are held for seconds and other writes queue | The service calls them first; the repo never makes network calls |
| Name a role or a schema in code, a migration (`OWNER TO …`, `schema.table`), a test or a seed script, or derive the role from the schema | Green here, then a deployer's own schema or role fails: the module switches to a role that does not exist (`500` on every route) or the migration stops on `must be able to SET ROLE` | The identity is `PG_USER` and `PG_SCHEMA`, nothing else; the migration runs as `PG_USER`, so it already owns what it creates. Tests take it from `backend/testdb`, seeds from `scripts/schema.sh`; `backend/module/identity_test.go` fails on any fixed name |
| Publish to production subjects in a consumer test | Fails at random while the component's container runs: the container consumes the test message and writes to the demo database | Start the consumer with test-private subjects (`start(…, subjects{…})`) |
| Change a comment or option in `contracts/crm/opportunity/v1/opportunity.proto` without tagging the contract package | Builds pass locally (the `replace` hides it); a shell fetching `crm-opportunity/v2` compiles against the old `gen/` | Any change under `gen/crm/opportunity/` needs a new contract-package tag and the root `go.mod` requiring it |

## Before changing code

1. Is this about the opportunity (here), the order (`erp/sales`) or the customer (`mdm/customer`)? Only the first goes here.
2. Am I adding a synchronous edge to ERP or reading ERP's state? Stop: publish or consume an event instead (see `docs/design.md`).
3. Contract change? Append only: a new field, a new rpc, a new path. Never remove or retype a field, rpc, path or event subject.
4. Did `gen/` change? Then the contract package needs a new tag and `go.mod` must require it.
5. A new REST route is registered with `besdk.GET` / `POST` / `PATCH` and a permission key from `assembly.yaml`; a scoped read gets a real-database test with two users who must not see each other's rows.
6. New rule → write the failing test first (real database for anything SQL), then the code.
7. Bump `metadata.version` before the first change after a release; update BRICKKIT.md and `docs/design.md` in the same commit as the code.

<!-- brickkit:managed:begin lang=en -->
<!-- maintained by brickkit (init, add, remove, upgrade, skills update): edits between these markers are overwritten -->

## BrickKit

This is a BrickKit component: `component.yaml` is all the platform reads. The rules it relies on:

- `configSchema` keys are the environment variable names the code reads. Never use a reserved name: `COMPONENT_ID`, `COMPONENT_VERSION`, `PORT`, `BRICKKIT_SERVED_MEMBERS`, `BRICKKIT_SERVED_MEMBERS_CONFIG`, or any `*_ENDPOINT`.
- Dependencies are exact versions. A dependency's address arrives as `<ID>_ENDPOINT`; an optional dependency that is absent has no variable at all, so read it with a fallback.
- `/healthz` checks only this process, never a dependency. The migration command runs from the same image and must fail on an argument it does not know.
- `BRICKKIT.md` travels to every project that uses this component and is read there without the repository: keep it in step with the code, with no relative links.
- Release: raise `metadata.version`, commit, push, `brickkit release`. `brickkit lint` checks the manifest and these docs — inside a project, run in this directory, it checks only this component (`--all` for the whole project).
- The full rules are in the `brickkit-component` skill (`.claude/skills/brickkit-component/SKILL.md` at the root of the project or repository where skills are installed; `brickkit skills update` installs it); for flags ask `brickkit <command> --help`; BrickKit's own documentation is `brickkit docs`.
<!-- brickkit:managed:end -->
