-- +goose Up
-- A user can have several chat accounts (Discord, Telegram, ...), linked
-- with a one-time code (/link); they share searches, watches and the
-- dashboard. users.platform/external_id stay as the account the user was
-- created with.
CREATE TABLE accounts (
    platform     TEXT        NOT NULL,
    external_id  TEXT        NOT NULL,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    display_name TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (platform, external_id)
);
CREATE INDEX idx_accounts_user ON accounts (user_id);

INSERT INTO accounts (platform, external_id, user_id, display_name)
SELECT platform, external_id, id, display_name FROM users
WHERE platform IS NOT NULL AND external_id IS NOT NULL;

-- One-time link codes (only their SHA-256), like login tokens.
CREATE TABLE link_codes (
    code_hash  TEXT PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE link_codes;
DROP TABLE accounts;
