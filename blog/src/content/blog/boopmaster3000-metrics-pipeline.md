---
title: A very serious boop counter
description: A goofy dog-photo experiment grew into a full analytics pipeline, one free-tier constraint at a time.
pubDate: 2026-10-19
tags: [boopmaster3000, javascript, metrics]
draft: false
---

I've owned the domain `jbirdkerr.net` for more than 15 years at this point. For the past three years, it used my infamous Henrybeard photo as a placeholder. I wanted to do something goofy with it: take an ordinary dog photo, make it interactive, and see how far I could get with general-purpose JavaScript, free-tier hosting, and maybe a little python if I felt squirrely.

That turned into Boopmaster3000. Booping the snoot was the fun part. Naturally, the next question was: how many boops are happening?

## First, count the boops

I added PostHog so I could see what people were doing. Then ad blockers got in the way. If the browser blocked PostHog's script or requests, the events never made it there. And let me tell you: UBlock Origin does so with extreme prejudice.

So I put a Cloudflare Worker in the middle. The browser sends the analytics traffic to my own endpoint, and the worker proxies it on to PostHog. That workaround became `metrics-worker` and is a much more serious component than this project probably deserved.

## One boop, one request? Not on the free tier

Sending a separate metric every time someone did something was going to chew through free-tier limits. Instead, the browser batches events together before sending them. The worker can forward that batch to PostHog and process the events in the background, rather than making every boop its own round trip.

## PostHog isn't my dashboard database

I also wanted a live counter. Asking PostHog over and over to render a little dashboard didn't make much sense, so I added Supabase, backed by PostgreSQL, for the operational metrics. The worker sends batches to a database function that records the events and updates aggregate counts together. The dashboard reads those totals from the database, with a short edge cache to avoid needlessly asking on every refresh.

The shape of it is now roughly:

boop → browser batch → metrics-worker → PostHog + Supabase/Postgres → dashboard

## A lot of infrastructure for a dog photo

None of this was the plan at the start. I wanted to make a placeholder photo do something silly, then answer one small question about it. Ad blockers led to a proxy. Free-tier limits led to batching. A dashboard led to a database and aggregate counters.

And that's how a goofy boop app acquired an analytics pipeline: JavaScript in the browser, a Cloudflare Worker at the edge, PostHog for event analytics, and Supabase/Postgres for the live numbers. It is a whole lot of machinery for counting snoot boops—and a pretty good tour of what you can build by following the next question.

Check out the [docs](https://github.com/jbirdkerr/jbirdkerr-net/tree/main/apps/metrics-worker/docs) for a more detailed look!
