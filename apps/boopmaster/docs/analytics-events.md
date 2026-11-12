# Analytics events

Events are sent with `captureEvent(name, properties)` in `js/main.js`. The PostHog SDK batches them and posts them to the metrics worker, which forwards them to PostHog and records them in the `metrics_worker` schema in Supabase (`record_site_events`). Some events also drive the aggregate counters shown in the stats panel.

## Events sent by this app

| Event | Properties | Sent when |
| --- | --- | --- |
| `boop` | `combo_count` (running count this page load), `via` (`click` or `api`) | Snoot click, `B`, or `boop()` |
| `eye_movement` | `distance_px` | Accumulated eye travel is flushed (about every 3 seconds with 10 px or more, on turning tracking off, and on page hide) |
| `feature_toggle` | `feature` (`eye_tracking`, `googly_eyes`, `party_mode`), `enabled`, and for some turn-on cases `trigger` (`click`, `double_click`, `key_p`) | A feature is switched on or off |
| `treat_shower` | `trigger` (`treat_command`) | `T` or `treat()` |
| `bark` | `trigger` (`key_w`) | `W` pressed. Barks from `bark()` or other paths are not tracked. |
| `github_click` | `url` | The GitHub link in the credits panel is clicked |

## What the stats panel counts

The worker's `record_site_events` function updates `site_metrics` from these events: total events, total boops, the maximum `combo_count`, turn-on toggles for eye tracking, googly eyes and party mode, treat showers, and the sum of `distance_px`. Per-event counts go to `site_event_counts`, and every event is also appended to `site_events`. See `db/supabase/migrations/` for the exact logic.

## PostHog configuration in the page

`js/main.js` initializes PostHog with request batching, web vitals and network timing capture, autocapture enabled with copied-text capture off, and `person_profiles: 'always'`. Autocapture, web vitals and persons are PostHog features and are not part of the event list above. Do not add further trackers or personal data (IP addresses, raw user agents) without an explicit request.

## Adding an event

1. Call `captureEvent('snake_case_name', { ... })` in `js/main.js`, matching the naming above.
2. Document it in the table here.
3. If it should feed the stats panel, that needs a change to the `record_site_events` function in a new timestamped migration and to the worker rendering. See `apps/metrics-worker/docs/`.
