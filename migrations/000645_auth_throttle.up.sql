-- Per-account sign-in throttles, shared by every API process and kept across restarts
-- (httpx.Limiter.Shared). key is the SHA-256 of the limiter key, so no address is stored.
CREATE TABLE auth_throttle (
    key          bytea PRIMARY KEY,
    window_start timestamptz NOT NULL,
    count        integer NOT NULL
);
CREATE INDEX auth_throttle_window_idx ON auth_throttle(window_start);
