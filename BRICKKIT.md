# crm/opportunity

## Purpose

The sales opportunity: who is selling what to which customer, at which stage, for how much and with what probability, and the moment it is won or lost. Winning publishes an event that `erp/sales` turns into an order; the order's ID comes back by event and is shown on the opportunity.

**Owns**

- Opportunities: name, customer (ID plus a copy of its name), lines (product snapshots, quantity, quoted unit price, subtotal), expected amount, probability (0–100, defaulted from the stage, overridable), expected close date, owner and department (snapshots of the creator's token), status `OPEN` / `WON` / `LOST` / `CANCELLED`, loss reason, `won_at`, and `order_id` (the order created from it, empty until that order's event arrives).
- The stage definitions (seeded by migration, no write API) and the stage history of every opportunity.
- The weighted amount (`expected_amount × probability / 100`, computed on read) and the per-stage funnel.
- The events `crm.opportunity.won.v1`, `crm.opportunity.lost.v1`, `crm.opportunity.stage_changed.v1`.

**Does not own**

- Orders, stock reservations, receivables and everything after "won": `erp/sales`, `erp/inventory`, `erp/finance`. This component never calls them; it only keeps the order ID from `erp/sales`' event.
- Customer and product master data: `mdm/customer`, `mdm/product`. The unit price on a line is a quote, not a price list; `erp/sales` prices the order itself.
- Activities, visits, leads: other CRM components.
- Conversion rates and forecasts: an analytics component fed by this component's events.

## Before you deploy

- **PostgreSQL**: a schema `crm_opportunity`, plus `crm_opportunity_archive` if your schema convention creates one (no job writes to it yet). A login role `crm_opportunity_rw` with `USAGE` and `CREATE` on `crm_opportunity`, and its password. The migration runs as this role and creates the tables and the stage definitions, so the role owns them; that matters because the running component creates weekly partitions of `event_outbox` / `event_inbox` itself, which needs ownership. BrickKit creates none of this; in the BrickEnterprise assembly project `make dev-env` writes the password into `.env` and `make db-init` creates the schemas, role and grants.
- **NATS** reachable at `NATS_URL`: events are written to an outbox table and pushed by a background loop, and the consumers of `mdm.customer.*` and `sales.order.created.v1` subscribe there. The component starts without NATS reachable; events wait in the outbox.
- **`mdm/customer` and `mdm/product`** running: creating an opportunity checks the customer and every product against them, synchronously.
- **Authorization** (`infra/authz`) and **identity** (`infra/iam-casdoor`, or any IAM serving a JWKS) reachable at `AUTHZ_BUNDLE_URL` and `IAM_JWKS_URL` for the REST routes to answer anything but errors. User tokens must carry `sub` and `dept_path`: they become the opportunity's owner and department and decide who sees it. A user with no department (`dept_path` empty) sees only the opportunities they own, and what they create has no department; to see the whole organisation, place the user in its root department. They are configuration, not dependencies: the component starts without them.
- Demo data (optional): `make seed` in the component directory first runs the seed of identity, authorization, customers and products, then creates eleven sample opportunities through the real REST API under two identities (a superuser and an East-region sales rep, to show the department scope). Three are won, so their events reach `erp/sales` when it runs; one of those is deliberately over the customer's credit limit, so `erp/sales` refuses its order.

## Dependencies

- `mdm/customer` (required): `BatchGet` when an opportunity is created, to check the customer exists and is active and to copy its name. The name copy is then kept current from `mdm.customer.created.v1` / `mdm.customer.updated.v1`.
- `mdm/product` (required): `BatchGet` when an opportunity is created, to check every product exists and is active and to copy SKU, name and base unit onto the lines.

Both are called over gRPC with the caller's token, with a 3-second timeout per call; a timeout answers `504`, an unreachable dependency `503`. There is no dependency on `erp/sales`: the two exchange only events (`crm.opportunity.won.v1` one way, `sales.order.created.v1` the other), so a project without ERP still runs this component; `order_id` simply stays empty.

The authorization bundle and the JWKS are fetched from the URLs in the configuration, not through a dependency edge. Without them every protected route fails closed: no or invalid token → `401`; `IAM_JWKS_URL` empty or unreachable → `403`; the bundle never fetched yet → `503`; a valid user without the permission key → `403`. `/healthz` stays `200` throughout: it reports only that this process is alive.

## Configuration

| Variable | Meaning |
|---|---|
| `PG_HOST` | PostgreSQL host. Usually the project's shared value (`$var:PG_HOST`). |
| `PG_PORT` | PostgreSQL port; default `5432`. |
| `PG_DATABASE` | The database holding the `crm_opportunity` schema (`$var:PG_DATABASE`). |
| `PG_USER` | The login role, `crm_opportunity_rw` as a literal. Inside a shell the shell logs in with its own role and switches to this one per transaction (`SET LOCAL ROLE`), so the role name must be `<PG_SCHEMA>_rw`. |
| `PG_PASSWORD` | Password of `PG_USER`. Secret: write `${CRM_OPPORTUNITY_DB_PASSWORD}` (or your secret store's reference), never the value. |
| `PG_SCHEMA` | Schema of all tables, the outbox and the migration state table (`schema_migrations_crm_opportunity`); default `crm_opportunity`. Write the literal anyway so every component's schema is visible in one place. |
| `NATS_URL` | NATS server for published and consumed events (`$var:NATS_URL`). |
| `OTEL_BASE_URL` | OpenTelemetry collector base URL; empty (the default) exports nothing. |
| `AUTHZ_BUNDLE_URL` | URL of the authorization bundle the permission check polls, for example `http://infra-authz-2-0-0:8223/authz/bundle`. Required: without it every protected route answers `503`. Keep it in step with the authz version the project runs. |
| `IAM_JWKS_URL` | URL of the JWKS used to verify user tokens locally, for example `http://infra-iam-casdoor-2-0-0:8200/.well-known/jwks.json`. Required: when it is empty or unreachable every protected route answers `403`. |

The addresses of `mdm/customer` and `mdm/product` are injected by the platform from the dependency declarations; they are not configuration keys.

## Contracts

- `contracts/crm/opportunity/v1/opportunity.proto` — gRPC `crm.opportunity.v1.OpportunityService` for other components: `CreateOpportunity`, `UpdateOpportunity` (full replacement; stale `version` → `ABORTED`), `ChangeStage`, `MarkWon`, `MarkLost` (each takes an `idempotency_key`), `GetOpportunity`, `ListOpportunities` (cursor paging, no offset), `BatchGetOpportunities` (missing IDs come back in `missing_ids`, not as an error), `ListStages`. `Opportunity` carries `order_id`. The Go package is the separate module `github.com/brickKit/crm-opportunity/gen/crm/opportunity`.
- `contracts/opportunity.openapi.yaml` — REST under `/crm/opportunity`, every route behind a permission key: `GET /opportunities` (`crm.opportunity.view`; `view=mine` for the caller's own, otherwise the caller's department and below; `customer_id`, `status_filter`, `stage_id_filter`; `created_after` / `created_before`, default the last 90 days; `cursor`, `page_size`), `GET /opportunities/{id}` (`crm.opportunity.view`; outside the caller's scope `404`, like an ID that does not exist), `GET /opportunities/{id}/stage-history` (`crm.opportunity.view`; outside the caller's scope `404`), `GET /opportunities/funnel` (`crm.opportunity.view`; per stage the count, expected and weighted sums of `OPEN` opportunities, with the same `view` switch as the list), `POST /opportunities` and `PATCH /opportunities/{id}` (`crm.opportunity.edit`; stale `version` → `409`), `POST /opportunities/{id}/stage` and `POST /opportunities/{id}/lose` (`crm.opportunity.edit`), `POST /opportunities/{id}/win` (`crm.opportunity.win`), `GET /stages` (`crm.opportunity.view`). The write routes answer `403` for an opportunity outside the caller's scope. Amounts are decimal strings; malformed amounts, quantities or probabilities (`NaN`, `1e3`, a negative amount, more decimals than the column holds) answer `400`. `BatchGetOpportunities` is not on REST.
- `contracts/events/opportunity.events.json` — events published through the outbox in the same transaction as the change: `crm.opportunity.won.v1` (on `MarkWon`; carries `owner_id` and `dept_path`, which the order inherits), `crm.opportunity.lost.v1`, `crm.opportunity.stage_changed.v1`. Each carries the opportunity's `version`. Consumed: `mdm.customer.created.v1` / `mdm.customer.updated.v1` (the customer name copy) and `sales.order.created.v1` from `erp/sales` (backfills `order_id` when its `source_opportunity_id` is one of these opportunities; an opportunity already linked to another order is logged and not overwritten).
- `assembly.yaml` — this project's metadata: the three permission keys `crm.opportunity.view` / `edit` / `win`, the menu entry, the edge route `/crm/opportunity/**`, the schema and role, and the data scopes `org` (`dept_path` prefix) and `owner` (`owner_id`) on `opportunities`.

## Shell declaration

Not a shell. It can be hosted in a Go shell (in the BrickEnterprise project, `be/go-backoffice`) or run on its own; the code is the same either way.
