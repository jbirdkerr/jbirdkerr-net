SET local check_function_bodies = off;

CREATE EXTENSION "postgis" SCHEMA "extensions";

CREATE POLICY "public can read site event counts" ON "public"."site_event_counts"
  FOR SELECT
  TO "anon", "authenticated"
  USING (true);

COMMENT ON EXTENSION "postgis" IS 'PostGIS geometry and geography spatial types and functions';
