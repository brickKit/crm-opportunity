-- crm/opportunity 3.0.0 (docs/design.md): opportunities, their stage history, quote requests to erp/sales and the
-- sales documents that came back.

CREATE TABLE opportunity (
    id                  uuid PRIMARY KEY,
    number              text NOT NULL UNIQUE,
    legal_entity_id     uuid NOT NULL,
    customer_id         uuid NOT NULL,
    customer_name       text NOT NULL,
    name                text NOT NULL,
    currency            text NOT NULL,
    amount              numeric(19,4) NOT NULL CHECK (amount >= 0),
    expected_close_date date,
    owner_id            text NOT NULL,
    stage               text NOT NULL CHECK (stage IN ('qualification', 'needs_analysis', 'proposal', 'negotiation',
                            'won', 'lost')),
    probability         int NOT NULL CHECK (probability BETWEEN 0 AND 100),
    -- OP06, OP07: the open stage and probability a closed opportunity reopens to
    stage_before_close       text,
    probability_before_close int,
    close_reason        text,
    closed_at           timestamptz,
    won_amount          numeric(19,4) CHECK (won_amount >= 0),
    search              text NOT NULL DEFAULT '',
    search_version      int NOT NULL DEFAULT 0,
    version             bigint NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK ((stage IN ('won', 'lost')) = (stage_before_close IS NOT NULL)),
    CHECK ((stage = 'won') = (won_amount IS NOT NULL))
);
CREATE INDEX opportunity_list ON opportunity (legal_entity_id, created_at DESC, id);
CREATE INDEX opportunity_owner ON opportunity (owner_id);

-- The products an opportunity suggests for a quotation (not checked here; erp/sales checks them).
CREATE TABLE opportunity_line (
    opportunity_id uuid NOT NULL REFERENCES opportunity (id),
    line_no        int NOT NULL,
    product_id     uuid NOT NULL,
    quantity       numeric(19,6) NOT NULL CHECK (quantity > 0),
    unit           text NOT NULL,
    PRIMARY KEY (opportunity_id, line_no)
);

-- One row per stage change, only appended (OP01, OP03, OP06, OP07, OP08).
CREATE TABLE stage_change (
    id             uuid PRIMARY KEY,
    opportunity_id uuid NOT NULL REFERENCES opportunity (id),
    from_stage     text,
    to_stage       text NOT NULL,
    reason         text,
    actor          text NOT NULL,
    trigger_kind   text NOT NULL CHECK (trigger_kind IN ('user', 'quotation', 'sales_order')),
    trigger_id     uuid,
    at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stage_change_of ON stage_change (opportunity_id, at);

-- Quote requests (OP05); the ID is the fact ID of crm.opportunity.quote_requested.v1.
CREATE TABLE quote_request (
    id             uuid PRIMARY KEY,
    opportunity_id uuid NOT NULL REFERENCES opportunity (id),
    lines          jsonb NOT NULL,
    state          text NOT NULL DEFAULT 'requested' CHECK (state IN ('requested', 'drafted', 'rejected')),
    quotation_id   uuid,
    reason         text,
    requested_by   text NOT NULL,
    requested_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX quote_request_of ON quote_request (opportunity_id, requested_at);

-- The erp/sales quotations and orders that carry an opportunity, by their last version seen (OP08).
CREATE TABLE sales_link (
    opportunity_id uuid NOT NULL REFERENCES opportunity (id),
    kind           text NOT NULL CHECK (kind IN ('quotation', 'sales_order')),
    document_id    uuid NOT NULL,
    number         text NOT NULL,
    state          text NOT NULL,
    net            numeric(19,4) NOT NULL,
    currency       text NOT NULL,
    version        bigint NOT NULL,
    PRIMARY KEY (kind, document_id)
);
CREATE INDEX sales_link_of ON sales_link (opportunity_id);
