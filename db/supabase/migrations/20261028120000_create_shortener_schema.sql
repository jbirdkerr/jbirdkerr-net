-- URL shortener: dedicated schema, tables, and privileges.
-- Aggregate-only by design: no IP addresses, user agents, cookies, or visitor ids.
CREATE SCHEMA IF NOT EXISTS "shortener";

REVOKE ALL ON SCHEMA "shortener" FROM PUBLIC;
GRANT USAGE ON SCHEMA "shortener" TO "service_role", "postgres";

-- API keys (hash only; the raw key is shown once at issuance and never stored)
CREATE TABLE "shortener"."api_keys" (
  "key_hash"   text    NOT NULL,
  "label"      text    NOT NULL,
  -- Phase 1 owner id. Links created with a key take this id.
  -- Phase 2 maps owners to Supabase auth.users ids (or leaves these as-is).
  "owner_id"   uuid    NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamp with time zone NOT NULL DEFAULT now(),
  "revoked"    boolean NOT NULL DEFAULT false,
  CONSTRAINT "api_keys_pkey" PRIMARY KEY (key_hash)
);

-- Links. Soft-delete only (disabled), so a slug is never reused.
CREATE TABLE "shortener"."links" (
  "slug"       text    NOT NULL,
  "target"     text    NOT NULL,
  "owner_id"   uuid    NOT NULL,
  "created_at" timestamp with time zone NOT NULL DEFAULT now(),
  "disabled"   boolean NOT NULL DEFAULT false,
  CONSTRAINT "links_pkey" PRIMARY KEY (slug),
  CONSTRAINT "links_slug_format" CHECK ((slug ~ '^[A-Za-z0-9-]{3,32}$'))
);

-- "Abc" and "abc" are confusable, so only one of them may exist.
CREATE UNIQUE INDEX "links_slug_lower_idx" ON "shortener"."links" (lower(slug));
CREATE INDEX "links_owner_id_idx" ON "shortener"."links" (owner_id);

-- Daily aggregate click counts
CREATE TABLE "shortener"."click_daily" (
  "slug"          text    NOT NULL,
  "day"           date    NOT NULL,
  "country"       text    NOT NULL DEFAULT 'unknown',
  "referrer_host" text    NOT NULL DEFAULT 'direct',
  "clicks"        integer NOT NULL DEFAULT 0,
  CONSTRAINT "click_daily_pkey" PRIMARY KEY (slug, day, country, referrer_host),
  CONSTRAINT "click_daily_slug_fkey" FOREIGN KEY (slug)
    REFERENCES "shortener"."links" (slug)
);

-- Fixed-window counters for per-key creation limits
CREATE TABLE "shortener"."rate_limits" (
  "key_hash"     text    NOT NULL,
  "window_start" timestamp with time zone NOT NULL,
  "hits"         integer NOT NULL DEFAULT 0,
  CONSTRAINT "rate_limits_pkey" PRIMARY KEY (key_hash, window_start)
);

-- Only the service role (the Worker) touches these tables, through PostgREST.
ALTER TABLE "shortener"."api_keys"    ENABLE ROW LEVEL SECURITY;
ALTER TABLE "shortener"."links"       ENABLE ROW LEVEL SECURITY;
ALTER TABLE "shortener"."click_daily" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "shortener"."rate_limits" ENABLE ROW LEVEL SECURITY;

GRANT SELECT ON TABLE "shortener"."api_keys" TO "service_role", "postgres";
GRANT SELECT, INSERT, UPDATE ON TABLE "shortener"."links" TO "service_role", "postgres";
GRANT SELECT ON TABLE "shortener"."click_daily" TO "service_role", "postgres";
GRANT SELECT ON TABLE "shortener"."rate_limits" TO "service_role", "postgres";
