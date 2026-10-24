CREATE TABLE IF NOT EXISTS profiles (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    skills      TEXT NOT NULL DEFAULT '',
    keywords    TEXT[] NOT NULL DEFAULT '{}',
    minus_words TEXT[] NOT NULL DEFAULT '{}',
    min_budget  INT NOT NULL DEFAULT 0,
    threshold   DOUBLE PRECISION NOT NULL DEFAULT 0.6,
    pitch_notes TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sources (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    interval_sec INT NOT NULL DEFAULT 180,
    last_run_at  TIMESTAMPTZ,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders (
    id            BIGSERIAL PRIMARY KEY,
    source_id     BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    external_id   TEXT NOT NULL,
    url           TEXT NOT NULL,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    category      TEXT NOT NULL DEFAULT '',
    budget        TEXT NOT NULL DEFAULT '',
    budget_value  INT,
    published_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    profile_id    BIGINT REFERENCES profiles(id) ON DELETE SET NULL,
    score         DOUBLE PRECISION,
    status        TEXT NOT NULL DEFAULT 'new',
    error         TEXT NOT NULL DEFAULT '',
    pitch         TEXT NOT NULL DEFAULT '',
    feedback      SMALLINT NOT NULL DEFAULT 0,
    feedback_at   TIMESTAMPTZ,
    tg_chat_id    BIGINT,
    tg_message_id BIGINT,
    notified_at   TIMESTAMPTZ,
    UNIQUE (source_id, external_id)
);

CREATE INDEX IF NOT EXISTS orders_created_idx ON orders (created_at DESC);
CREATE INDEX IF NOT EXISTS orders_status_idx ON orders (status);

CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

ALTER TABLE profiles ADD COLUMN IF NOT EXISTS pitch_template TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN IF NOT EXISTS pattern TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN IF NOT EXISTS cookie TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS matched TEXT[] NOT NULL DEFAULT '{}';

-- Additive migration: legacy orders/profiles remain shared team data.
CREATE TABLE IF NOT EXISTS users (
 id BIGSERIAL PRIMARY KEY,
 username TEXT UNIQUE NOT NULL,
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 telegram_chat_id BIGINT UNIQUE,
 notify_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 all_profiles BOOLEAN NOT NULL DEFAULT TRUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);
CREATE TABLE IF NOT EXISTS login_limits (
 key TEXT PRIMARY KEY,
 attempts INT NOT NULL,
 reset_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS telegram_links (
 token_hash TEXT PRIMARY KEY,
 user_id BIGINT UNIQUE NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS subscriptions (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id BIGINT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 PRIMARY KEY(user_id,profile_id)
);
CREATE TABLE IF NOT EXISTS deliveries (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
 chat_id BIGINT NOT NULL,
 message_id BIGINT NOT NULL,
 PRIMARY KEY(user_id,order_id)
);
