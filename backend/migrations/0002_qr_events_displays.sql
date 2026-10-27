-- Stage 1-3: table QR tokens, committed-change event log (for SSE replay), TV display devices.
-- Backward compatible: only adds columns/tables. Existing tables get a random QR token.

ALTER TABLE dining_tables ADD COLUMN qr_token TEXT;
ALTER TABLE dining_tables ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;
UPDATE dining_tables SET qr_token = replace(gen_random_uuid()::text,'-','') || replace(gen_random_uuid()::text,'-','') WHERE qr_token IS NULL;
ALTER TABLE dining_tables ALTER COLUMN qr_token SET NOT NULL;
ALTER TABLE dining_tables ALTER COLUMN qr_token SET DEFAULT (replace(gen_random_uuid()::text,'-','') || replace(gen_random_uuid()::text,'-',''));
CREATE UNIQUE INDEX dining_tables_qr_token_key ON dining_tables(qr_token);

-- Revocable, scoped display access for the public TV. Only a hash of the token is stored.
CREATE TABLE display_devices (
  id           BIGSERIAL PRIMARY KEY,
  branch_id    BIGINT NOT NULL REFERENCES branches(id),
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  created_by   BIGINT REFERENCES staff_users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ
);

-- One row per committed order change, written in the SAME transaction as the change.
-- Contains no personal data. Clients treat events as "something changed" and re-read the API.
CREATE TABLE events (
  id             BIGSERIAL PRIMARY KEY,
  branch_id      BIGINT NOT NULL REFERENCES branches(id),
  type           TEXT NOT NULL,
  order_id       BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  order_number   BIGINT NOT NULL,
  order_type     TEXT NOT NULL,
  status         TEXT NOT NULL,
  payment_status TEXT NOT NULL,
  table_label    TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_branch_id_idx ON events(branch_id, id);
CREATE INDEX events_created_idx ON events(created_at);
