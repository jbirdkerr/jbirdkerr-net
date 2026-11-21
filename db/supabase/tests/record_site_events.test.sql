-- metrics_worker.record_site_events: stores each event, maintains per-event counts,
-- and folds Boopmaster events into the singleton site_metrics row. Runs in a
-- transaction that is rolled back.
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgtap WITH SCHEMA extensions;
SET search_path TO extensions, public;

SELECT plan(19);

-- Known starting state, independent of seed data.
DELETE FROM metrics_worker.site_events;
DELETE FROM metrics_worker.site_event_counts;
INSERT INTO metrics_worker.site_metrics (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
UPDATE metrics_worker.site_metrics SET
  total_boops = 0, max_boop_combo = 0, total_events = 0, eye_tracking_toggles = 0,
  googly_eyes_toggles = 0, party_mode_toggles = 0, treat_showers = 0,
  total_eye_distance = 0, updated_at = '2000-01-01'
WHERE id = true;

SELECT throws_ok(
  $$SELECT metrics_worker.record_site_events('{"event": "boop"}'::jsonb)$$,
  'p_events must be a JSON array',
  'a non-array payload is rejected'
);

-- Shaped like the worker's normalize_posthog_payload() output.
SELECT lives_ok($$
  SELECT metrics_worker.record_site_events($json$[
    {"event": "boop", "properties": {"combo_count": 3}, "occurred_at": "2026-11-20T12:00:00Z", "distinct_id": "u1"},
    {"event": "boop", "properties": {"combo_count": 7}},
    {"event": "boop", "properties": {"combo_count": 5}},
    {"event": "feature_toggle", "properties": {"feature": "eye_tracking", "enabled": true}},
    {"event": "feature_toggle", "properties": {"feature": "eye_tracking", "enabled": false}},
    {"event": "feature_toggle", "properties": {"feature": "googly_eyes", "enabled": true}},
    {"event": "feature_toggle", "properties": {"feature": "party_mode", "enabled": true}},
    {"event": "treat_shower", "properties": {}},
    {"event": "eye_movement", "properties": {"distance_px": 120}},
    {"event": "eye_movement", "properties": {"distance_px": ""}},
    {"event": "$pageview", "occurred_at": "not a timestamp"},
    {"event": "", "properties": {}},
    {"properties": {"orphan": true}}
  ]$json$::jsonb)
$$, 'a mixed batch is recorded without error');

SELECT is((SELECT count(*) FROM metrics_worker.site_events), 11::bigint,
  'events without a name are skipped');
SELECT is(
  (SELECT count FROM metrics_worker.site_event_counts WHERE event = 'boop'), 3::bigint,
  'per-event counts accumulate');
SELECT is(
  (SELECT count FROM metrics_worker.site_event_counts WHERE event = 'feature_toggle'), 4::bigint,
  'every toggle event is counted, enabled or not');

SELECT is(total_events, 11::bigint, 'total_events') FROM metrics_worker.site_metrics;
SELECT is(total_boops, 3::bigint, 'total_boops') FROM metrics_worker.site_metrics;
SELECT is(max_boop_combo, 7::bigint, 'max_boop_combo keeps the highest combo_count') FROM metrics_worker.site_metrics;
SELECT is(eye_tracking_toggles, 1::bigint, 'only enabling eye tracking counts') FROM metrics_worker.site_metrics;
SELECT is(googly_eyes_toggles, 1::bigint, 'googly eyes toggles') FROM metrics_worker.site_metrics;
SELECT is(party_mode_toggles, 1::bigint, 'party mode toggles') FROM metrics_worker.site_metrics;
SELECT is(treat_showers, 1::bigint, 'treat showers') FROM metrics_worker.site_metrics;
SELECT is(total_eye_distance, 120::bigint, 'eye distance sums distance_px; blank counts as 0') FROM metrics_worker.site_metrics;
SELECT ok(updated_at > '2000-01-01', 'updated_at moves forward') FROM metrics_worker.site_metrics;

SELECT is(
  (SELECT occurred_at FROM metrics_worker.site_events WHERE distinct_id = 'u1'),
  '2026-11-20T12:00:00Z'::timestamptz,
  'occurred_at is taken from the event');
SELECT ok(
  (SELECT occurred_at FROM metrics_worker.site_events WHERE event = '$pageview') >= now() - interval '1 minute',
  'an unparseable occurred_at falls back to now()');
SELECT is(
  (SELECT properties FROM metrics_worker.site_events WHERE event = '$pageview'), '{}'::jsonb,
  'missing properties are stored as an empty object');

-- A lower combo later must not lower the high score.
SELECT metrics_worker.record_site_events('[{"event": "boop", "properties": {"combo_count": 2}}]');
SELECT is(max_boop_combo, 7::bigint, 'a lower combo does not reduce max_boop_combo') FROM metrics_worker.site_metrics;

-- The function returns the updated metrics row.
SELECT is(
  (metrics_worker.record_site_events('[{"event": "boop"}]') ->> 'total_boops')::bigint,
  5::bigint,
  'returns the updated site_metrics row as JSON');

SELECT * FROM finish();
ROLLBACK;
