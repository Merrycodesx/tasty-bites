-- Tasty Bites initial schema. Money is stored as integer santim (1 ETB = 100 santim).
CREATE TABLE branches (
  id          BIGSERIAL PRIMARY KEY,
  name        TEXT NOT NULL,
  timezone    TEXT NOT NULL DEFAULT 'Africa/Addis_Ababa',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
  branch_id BIGINT NOT NULL REFERENCES branches(id),
  key       TEXT NOT NULL,
  value     TEXT NOT NULL,
  PRIMARY KEY (branch_id, key)
);

CREATE TABLE staff_users (
  id            BIGSERIAL PRIMARY KEY,
  branch_id     BIGINT NOT NULL REFERENCES branches(id),
  username      TEXT NOT NULL UNIQUE,
  full_name     TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('admin','manager','waiter','cashier','kitchen')),
  active        BOOLEAN NOT NULL DEFAULT true,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE menu_categories (
  id         BIGSERIAL PRIMARY KEY,
  branch_id  BIGINT NOT NULL REFERENCES branches(id),
  name       TEXT NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  active     BOOLEAN NOT NULL DEFAULT true,
  UNIQUE (branch_id, name)
);

CREATE TABLE menu_items (
  id          BIGSERIAL PRIMARY KEY,
  branch_id   BIGINT NOT NULL REFERENCES branches(id),
  category_id BIGINT NOT NULL REFERENCES menu_categories(id),
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  price_santim BIGINT NOT NULL CHECK (price_santim >= 0),
  available   BOOLEAN NOT NULL DEFAULT true,
  archived    BOOLEAN NOT NULL DEFAULT false,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX menu_items_category_idx ON menu_items(category_id);

-- A variant replaces the base price (e.g. Small / Large). Extras add to it.
CREATE TABLE item_variants (
  id           BIGSERIAL PRIMARY KEY,
  item_id      BIGINT NOT NULL REFERENCES menu_items(id),
  name         TEXT NOT NULL,
  price_santim BIGINT NOT NULL CHECK (price_santim >= 0),
  available    BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE item_extras (
  id           BIGSERIAL PRIMARY KEY,
  item_id      BIGINT NOT NULL REFERENCES menu_items(id),
  name         TEXT NOT NULL,
  price_santim BIGINT NOT NULL CHECK (price_santim >= 0),
  available    BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE dining_tables (
  id        BIGSERIAL PRIMARY KEY,
  branch_id BIGINT NOT NULL REFERENCES branches(id),
  label     TEXT NOT NULL,
  seats     INT NOT NULL DEFAULT 4,
  UNIQUE (branch_id, label)
);

CREATE SEQUENCE order_number_seq START 1001;

CREATE TABLE orders (
  id             BIGSERIAL PRIMARY KEY,
  branch_id      BIGINT NOT NULL REFERENCES branches(id),
  order_number   BIGINT NOT NULL DEFAULT nextval('order_number_seq') UNIQUE,
  public_token   TEXT NOT NULL UNIQUE DEFAULT replace(gen_random_uuid()::text, '-', ''),
  source         TEXT NOT NULL CHECK (source IN ('customer','staff')),
  order_type     TEXT NOT NULL CHECK (order_type IN ('dine_in','takeaway','delivery')),
  table_id       BIGINT REFERENCES dining_tables(id),
  customer_name  TEXT NOT NULL DEFAULT '',
  customer_phone TEXT NOT NULL DEFAULT '',
  delivery_address TEXT NOT NULL DEFAULT '',
  notes          TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL CHECK (status IN ('placed','preparing','ready','served','completed','cancelled')),
  payment_status TEXT NOT NULL DEFAULT 'unpaid' CHECK (payment_status IN ('unpaid','pending','paid','failed','cancelled','refunded')),
  subtotal_santim BIGINT NOT NULL CHECK (subtotal_santim >= 0),
  total_santim    BIGINT NOT NULL CHECK (total_santim >= 0),
  created_by     BIGINT REFERENCES staff_users(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_status_idx ON orders(branch_id, status, created_at);

-- Names and prices are snapshots taken at order time.
CREATE TABLE order_items (
  id              BIGSERIAL PRIMARY KEY,
  order_id        BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  menu_item_id    BIGINT NOT NULL REFERENCES menu_items(id),
  name            TEXT NOT NULL,
  variant_name    TEXT NOT NULL DEFAULT '',
  quantity        INT NOT NULL CHECK (quantity > 0),
  unit_price_santim BIGINT NOT NULL CHECK (unit_price_santim >= 0),
  line_total_santim BIGINT NOT NULL CHECK (line_total_santim >= 0),
  note            TEXT NOT NULL DEFAULT '',
  kitchen_status  TEXT NOT NULL DEFAULT 'queued' CHECK (kitchen_status IN ('queued','preparing','ready'))
);
CREATE INDEX order_items_order_idx ON order_items(order_id);

CREATE TABLE order_item_options (
  id             BIGSERIAL PRIMARY KEY,
  order_item_id  BIGINT NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  price_santim   BIGINT NOT NULL CHECK (price_santim >= 0)
);

CREATE TABLE order_status_history (
  id         BIGSERIAL PRIMARY KEY,
  order_id   BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  from_status TEXT,
  to_status  TEXT NOT NULL,
  actor_id   BIGINT REFERENCES staff_users(id),
  reason     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payments (
  id           BIGSERIAL PRIMARY KEY,
  order_id     BIGINT NOT NULL REFERENCES orders(id),
  provider     TEXT NOT NULL CHECK (provider IN ('cash','telebirr')),
  amount_santim BIGINT NOT NULL CHECK (amount_santim > 0),
  status       TEXT NOT NULL CHECK (status IN ('pending','paid','failed','cancelled','refunded')),
  provider_ref TEXT,
  recorded_by  BIGINT REFERENCES staff_users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_ref)
);

CREATE TABLE idempotency_records (
  key         TEXT NOT NULL,
  scope       TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  order_id    BIGINT REFERENCES orders(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, key)
);

CREATE TABLE audit_logs (
  id         BIGSERIAL PRIMARY KEY,
  actor_id   BIGINT REFERENCES staff_users(id),
  action     TEXT NOT NULL,
  entity     TEXT NOT NULL,
  entity_id  BIGINT,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
