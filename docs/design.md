[English](design.md) · [中文](design.zh.md)

# crm/opportunity design

Conclusions only, for whoever changes this component's design. How to use it: `BRICKKIT.md`; how the code is laid out: `AGENTS.md`.

## Boundaries

This component owns the sales opportunity: who is selling what to which customer, at which stage, for how much and with what probability, and the moment it is won or lost. Winning is the trigger of the order chain: this component announces the fact and does not care what happens next.

| Not here | Owner | Why |
|---|---|---|
| Orders, stock reservations, receivables | `erp/sales`, `erp/inventory`, `erp/finance` | Everything after "won" belongs to ERP. Whether the order could be created, whether stock was short, whether a compensation ran: this component does not know and must not know |
| Customer master data | `mdm/customer` | This component keeps `customer_id` and a display copy of the name |
| Products and prices | `mdm/product`, `erp/sales` | The unit price on an opportunity line is the price quoted during the negotiation, not a price list; `erp/sales` prices the order with its own engine and the two may differ |
| Activities, visits, calls | a future CRM activity component | Another aggregate root; no `last_contact_note` field on `opportunities` |
| Leads | a future CRM lead component | Converting a lead into an opportunity happens there |

The one piece of order information this component keeps is `order_id`, the ID of the order created from a won opportunity. It is written only by consuming `erp/sales`' own event (see Events), never by asking ERP, so CRM still has no synchronous edge to ERP and does not follow the order's state machine (confirmed, shipped, cancelled stay in `erp/sales`).

## Owned data

| Table | Partitioned | Notes |
|---|---|---|
| `opportunities` | no | `customer_id` + display `customer_name`; `owner_id`, `dept_id`, `dept_path` are snapshots taken at creation from the creator's token; `stage_id`; `expected_amount NUMERIC(18,2)`; `probability` 0–100; `expected_close_date`; `status`; `currency` (single currency, default `CNY`); `lost_reason`; `won_at`; `order_id` (empty until the order event arrives); `version` for optimistic locking |
| `opportunity_items` | no | product, SKU, name and unit snapshots, `qty NUMERIC(18,6)`, `quoted_unit_price`, `subtotal` computed on write |
| `opportunity_stage_history` | no | append-only: from stage, to stage, the probability set by the change, who, when. Rows written before the `probability` column existed have it `NULL` |
| `opportunity_stages` | no | stage definitions seeded by migration (`INITIAL_CONTACT` 10 %, `NEEDS_CONFIRMED` 30 %, `PROPOSAL` 50 %, `NEGOTIATION` 70 %, `WON` 100 %); stages are data, not code, and there is no write API |
| `customer_snapshots` | no | the customer name, kept current from `mdm.customer.*` events |
| `command_idempotency` | no | `idempotency_key` primary key, the opportunity ID of the first execution |
| `event_outbox`, `event_inbox` | weekly, by `created_at` | partitions are created four weeks ahead by the component's own loop; without it inserts fail from the fifth week with "no partition of relation found for row" |

Terminal states: `WON`, `LOST`, `CANCELLED`. Active: `OPEN`. Only `OPEN` can be updated, moved to another stage, won or lost.

The weighted amount (`expected_amount × probability / 100`) is computed on read and never stored: a stored value drifts as soon as the amount or the probability changes. All amount arithmetic is decimal (exact rationals, rounded to the cent half away from zero, the same rule as PostgreSQL's `round(numeric, 2)`), never `float64`.

## Contract surface

gRPC `crm.opportunity.v1.OpportunityService`, for other components:

| rpc | Kind | Notes |
|---|---|---|
| `CreateOpportunity` | write | idempotent; validates customer and products against master data; starts at the stage with the smallest `sort_order`; probability from the stage unless given (`optional`, so 0 is a valid override) |
| `UpdateOpportunity` | write | idempotent; `name` and `expected_amount` are required (empty → `INVALID_ARGUMENT`); an omitted `owner_id` or `expected_close_date` keeps its current value; stale `version` → `ABORTED`. The two optional fields keep rather than clear because a caller that sends only what it changes, as the contract allows, must not wipe the owner: without an owner the opportunity drops out of `view=mine`, a later win publishes `owner_id: ""`, and a row created by someone with no department (`dept_path` empty) becomes visible to nobody. The cost is that a close date, once set, cannot be cleared through this call |
| `ChangeStage` | write | idempotent; any stage to any stage, history row written |
| `MarkWon` | write | idempotent; sets `WON`, probability 100, `won_at`, and writes `crm.opportunity.won.v1` in the same transaction |
| `MarkLost` | write | idempotent; reason required |
| `GetOpportunity`, `ListOpportunities` | read | `List` pages by cursor; `created_after` / `created_before` give the creation-time window, default the last 90 days |
| `BatchGetOpportunities` | read | the batched read callers use instead of a loop of `Get`; missing IDs in `missing_ids` |
| `ListStages` | read | the stage definitions |

`Opportunity` carries `order_id` (empty until backfilled).

**Numeric input is validated with regular expressions before anything is written or any dependency is called**: `expected_amount` and `quoted_unit_price` are non-negative plain decimals with at most 16 integer digits and 2 decimals (`NUMERIC(18,2)`); `qty` is a positive plain decimal with at most 12 integer digits and 6 decimals (`NUMERIC(18,6)`); a probability override is 0–100. Anything else, `NaN`, `Inf`, `1e3`, `-1`, `.5`, `100.005` included, is `InvalidArgument` (`400`). The database cannot be the check: PostgreSQL's `NUMERIC` stores `'NaN'` (which sorts above every number, so `CHECK`s pass and the funnel sums turn into `NaN`), accepts exponents, rounds extra decimals silently, and reports overflow or a violated `CHECK` as a server error (`500`). `strconv.ParseFloat` accepts `NaN`, `Inf` and exponents too.

REST under `/crm/opportunity`:

| Route | Permission key |
|---|---|
| `GET /opportunities`, `GET /opportunities/{id}` | `crm.opportunity.view` |
| `GET /opportunities/funnel`, `GET /opportunities/{id}/stage-history` | `crm.opportunity.view` |
| `POST /opportunities`, `PATCH /opportunities/{id}` | `crm.opportunity.edit` |
| `POST /opportunities/{id}/stage`, `POST /opportunities/{id}/lose` | `crm.opportunity.edit` |
| `POST /opportunities/{id}/win` | `crm.opportunity.win` |
| `GET /stages` | `crm.opportunity.view` |

Winning has its own key: it starts order creation, stock reservation and receivables, the only action here with financial consequences downstream. `BatchGetOpportunities` is gRPC only.

**Errors on REST** follow the SDK's gRPC → HTTP table: invalid input (including a malformed `created_after` / `created_before` on the list) and an action on a terminal opportunity → `400`; an opportunity outside the caller's data scope on the write routes → `403`; not found, or outside the caller's data scope on `GET /opportunities/{id}` and `stage-history` → `404`; stale `version` on `PATCH` → `409` (`ABORTED`, declared in the OpenAPI contract); a master-data call that timed out → `504`. Errors the caller can correct are logged at `INFO`, timeouts and unavailable dependencies at `WARN`, only internal errors at `ERROR`.

**Stage history** (`GET /opportunities/{id}/stage-history`): every stage change of one opportunity, oldest first, `{from_stage_id, to_stage_id, probability, changed_by, changed_at}`. An opportunity outside the caller's data scope answers `404`, exactly like one that does not exist, so the route does not reveal which IDs exist. Not paged: it is a child collection of one opportunity, bounded by how often one deal changes stage. The creation itself is not a history row (there is no "from").

**Funnel** (`GET /opportunities/funnel`): per stage, in `sort_order`, the count, the sum of `expected_amount` and the sum of weighted amounts of `OPEN` opportunities; every stage is listed, with zeros where there is none. The weighted sum is the sum of each opportunity's weighted amount rounded to the cent, so it equals the sum of what the list shows. `view` is the same switch as on the list: `mine` counts the caller's own opportunities (`owner_id` = the caller), anything else counts the caller's department and below (`dept_path` prefix). It is a switch, not an `OR`. No time window: a pipeline is every open deal, however old.

There is no status endpoint for timed-out writes: no component writes here synchronously.

## Events

Published, all through the outbox in the same transaction as the state change:

| Subject | When | Payload |
|---|---|---|
| `crm.opportunity.won.v1` | `MarkWon` | `opportunity_id`, `customer_id`, `items[]` (`product_id`, `quantity`, `quoted_unit_price`), `owner_id`, `dept_path`, `currency`, `expected_close_date`, `revision` |
| `crm.opportunity.lost.v1` | `MarkLost` | `opportunity_id`, `reason`, `amount` |
| `crm.opportunity.stage_changed.v1` | `ChangeStage` | `opportunity_id`, `from_stage`, `to_stage` |

`owner_id` and `dept_path` on the won event are what `erp/sales` uses as the order's owner and department: it creates the order in an event handler, with no user token, and the order inherits the opportunity's ownership. Without them the order exists but nobody's list shows it. `quoted_unit_price` is informative only. The envelope `version` is the opportunity's `version` after the change.

Consumed:

| Subject | From | What it does | Duplicates and order |
|---|---|---|---|
| `mdm.customer.created.v1`, `mdm.customer.updated.v1` | `mdm/customer` | refreshes `customer_snapshots.name` | applied only when the event's version is greater than the stored one |
| `sales.order.created.v1` | `erp/sales` | backfills `order_id` | see below |

**`order_id` backfill.** `erp/sales` publishes `sales.order.created.v1` when an order is confirmed, with an optional `source_opportunity_id` (empty or absent for orders that did not come from an opportunity). The consumer reads only `order_id` and `source_opportunity_id`, tolerantly: a missing field is an empty string, an empty `source_opportunity_id` means "not ours" and the event is ignored, and so is an ID that is not an opportunity of this component. Otherwise, in one transaction with the inbox row:

- `order_id` empty → set it (version and `updated_at` move on, no event is published);
- `order_id` already equal → nothing (a redelivery or a replay);
- `order_id` set to another order → logged at `ERROR`, not overwritten: one opportunity is converted once, and a second order claiming the same opportunity is a data problem to look at, not something to resolve silently.

Duplicates of the same order event are also dropped by the inbox (subject, order ID, version). The subject lacks the `erp.` domain prefix that `<domain>.<aggregate>.<action>` asks for; it is kept as published, because renaming a subject is removing one.

## Dependencies

| Dependency | Used for |
|---|---|
| `mdm/customer` | `BatchGet` when an opportunity is created: the customer exists and is `ACTIVE`, and its name for the snapshot |
| `mdm/product` | `BatchGet` when an opportunity is created: every product exists and is `ACTIVE`, and SKU, name and base unit for the line snapshots |

Both are called with the caller's token (`UserClient`), never as the component itself, with a 3-second timeout per call. Blocking bad data at creation is the reason the edges are synchronous: reconciliation afterwards cannot undo an opportunity on a customer that never existed.

Expected but absent:

| Not a dependency | Why |
|---|---|
| `erp/sales` | CRM and ERP have no synchronous edge, only events. A synchronous "create the order" in `MarkWon` would make winning fail when stock is short, although the deal was won; a CRM-only installation could not be assembled; and the order would be a step of the opportunity's transaction. The order ID comes back by event instead |
| `erp/inventory`, `erp/finance` | further still: an opportunity does not know stock or receivables exist |
| `infra/workflow` | opportunities are not approved. A future rule such as "large wins need approval" would make this component call the workflow, not the other way round |
| `infra/iam-casdoor`, `infra/authz` | tokens are verified locally against `IAM_JWKS_URL`, permissions come from the bundle at `AUTHZ_BUNDLE_URL`: configuration, not edges |

## Place in the synchronous call graph

Two outgoing edges (`mdm/customer`, `mdm/product`), both to master data that calls no one, so every path from here has length one and there is no cycle. Incoming: the frontend and the mobile BFF over REST. No component calls it synchronously; `erp/sales` hears from it only through `crm.opportunity.won.v1`, and it hears from `erp/sales` only through `sales.order.created.v1`.

## Partitioning and archiving

`opportunities`, `opportunity_items` and `opportunity_stage_history` are not partitioned: one row per sales opportunity is one to two orders of magnitude fewer rows than transaction ledgers. `event_outbox` and `event_inbox` are partitioned weekly.

Archiving rule: a terminal opportunity (`WON`, `LOST`, `CANCELLED`) older than 24 months moves to `<PG_SCHEMA>_archive`, with its lines and history; `OPEN` opportunities stay hot however old. The window is longer than the order ledger's because sales analysis compares a quarter with the same quarter a year earlier. `opportunity_stages` and `customer_snapshots` are never archived.

## Database identity

The component knows no database names. The schema is `PG_SCHEMA` and the role every transaction switches to (`SET LOCAL ROLE`) is `PG_USER`, both from configuration; the migrations name neither, and unqualified table names resolve through the search path the migration entry sets. Standalone, `PG_USER` is the role the pool logs in with; in a shell the pool logs in as the shell and each member switches to its own `PG_USER`. `module.New` refuses to start when either is missing or is not a plain lower-case identifier, so a wrong name fails at start, not on the first request.

Earlier the role was derived as `<PG_SCHEMA>_rw` and the migrations ended every `CREATE TABLE` with `OWNER TO crm_opportunity_rw`. That made `PG_SCHEMA` configurable in name only: any other schema switched to a role that did not exist, and any other login role failed the migration on the hard-coded owner. Both were wrong in design, not just in code: a component must be deployable into whatever schema and role a deployer provides, and the database must be swappable. The `OWNER TO` lines were removed from the already applied migrations 001 and 002; existing databases are unaffected, because an applied migration is never run again and their tables already belong to that role (`make test-db-init` left every owner unchanged in both the test and the demo database). `backend/module/identity_test.go` proves the result: it creates a schema and a login role with unrelated names in the test database, migrates as that role, checks that the role owns every table, and through `module.New` lets the background loop create partitions and writes and reads an opportunity, both standalone and as a shell member.

The tests follow the same rule: their schema and role come from `backend/testdb` (`TEST_PG_SCHEMA` / `TEST_PG_ROLE`, by default the identity `make test-db-init` creates), and the seed scripts read each component's schema from the project's `config/`.

## Data scopes

Two dimensions on `opportunities`: `org` (`dept_path` prefix) and `owner` (`owner_id` equals). They are the same two as `erp/sales`, on purpose: an order inherits the opportunity's ownership, and mismatched dimensions would let someone see the opportunity but not the order made from it.

| Route | How the scope applies |
|---|---|
| `GET /opportunities`, `GET /opportunities/funnel` | `view=mine` → `owner_id` = the caller's `sub`; otherwise `dept_path` starts with the caller's `dept_path`. A switch: one operand at a time, both from the caller's token |
| `GET /opportunities/{id}`, `GET /opportunities/{id}/stage-history` | visible when `dept_path` starts with the caller's `dept_path` **or** `owner_id` is the caller: both operands come from the caller's token; outside → `404`, exactly like an ID that does not exist |
| the write routes (`PATCH`, `stage`, `win`, `lose`) | the same visibility as the single read; outside → `403`: whoever writes already holds the ID |

**A caller without a department.** A real department path is always `/<id>/…/`; the root department is `/<root id>/` too. An empty `dept_path` in the token means only one thing: this person has not been placed in any department yet. The SDK (be-sdk-go v0.5.0) then gives a prefix that matches nothing (`besdk.NoDeptPath`) and `HasDept = false`, so the `org` dimension matches no row: the department view of the list is empty, the department funnel is zero, and single reads and writes are left with "I am the owner". Seeing the whole organisation means sitting in its root department; `"/"` is the explicit marker for every tree and, as an ordinary prefix, matches every opportunity that has a department.

Such a person may still create opportunities. The ownership snapshot then writes an empty `dept_id` and `dept_path`, never the SDK's marker: a row with an empty `dept_path` belongs to no department, so only its owner sees it, and neither `"/"` nor the root department matches it. The won event carries that empty `dept_path`, and the order made from it is likewise visible only to its owner.

The repository does not trust its callers with this either: an empty prefix in `InScope` matches no department, and the department view of the list or the funnel with an empty prefix is an `InvalidArgument`, not "no limit". An empty prefix can only mean the caller forgot to fill it in.

## Reference implementations

| Project | Version | Module consulted | What was borrowed | License | Usage |
|---|---|---|---|---|---|
| Odoo | 17.0 | `addons/crm/models/crm_lead.py`, `crm_stage.py` | `probability` 0–100 defaulted from the stage and overridable by hand; expected revenue; weighted amount as the product, not stored; a loss reason is required; stages stored as data | LGPL-3 | Borrowed reasoning |
| ERPNext | — | `Opportunity` doctype | which snapshot fields an opportunity line carries | GPL-3 | Borrowed reasoning |
| Salesforce, SuiteCRM | — | opportunity stages and forecast | probability bound to the stage as a default | proprietary | Observed behaviour |

**Deliberately avoided**: Odoo closing an opportunity as won when its probability reaches 100: here winning is the explicit `MarkWon`, because it starts order creation and a number in a form must not. Odoo's single table for leads and opportunities: their lifecycles and data scopes differ. A CRM-side "converted / invoiced" status: that would mean asking ERP synchronously or following its state machine; only the order ID is kept, and the order's state stays in `erp/sales`.

Two real differences between products, both decided as customer forks rather than slots, because the component will be depended on by other CRM components and the first difference also lives in `erp/sales`: whether a won opportunity becomes an order automatically (here: automatically) and how strictly stages must follow each other (here: any stage to any stage).

## Open questions

| Question | Current answer |
|---|---|
| `order_id` is backfilled from `sales.order.created.v1`, which `erp/sales` publishes when the order is confirmed: an order stuck in draft (for example over the credit limit) leaves `order_id` empty | Accepted for now: "converted" means "a confirmed order exists". If screens need draft orders too, `erp/sales` adds an event for created drafts and this consumer subscribes to it as well |
| An opportunity created by someone without a department keeps an empty `dept_path` after they are placed in one | Accepted: the department is a snapshot taken at creation, and nothing records which department the row "should" have had. If it matters, a person changes its owner by hand (`PATCH`); the department column stays a snapshot |
| The gRPC server has no token verification: reads and writes that need the caller's data scope fail over gRPC, and `BatchGetOpportunities` applies no scope. Outgoing calls carry no token either: `besdk.UserClient` forwards only an `authorization` that arrived over gRPC, which a REST request does not have | The SDK has no gRPC authentication interceptor yet; REST is the user-facing surface. To be closed in the SDK, not here |
| No job archives terminal opportunities yet | The rule above stands; the job comes with the project's archiving work |
| Funnel conversion rates and forecasts | Only the per-stage sums live here. Conversion analysis belongs to an analytics component fed by `crm.opportunity.stage_changed.v1`, not to the business component |
| An opportunity whose customer does not exist yet in master data (prospects) | Not supported: the customer must exist. Prospects belong to a future CRM customer component |
