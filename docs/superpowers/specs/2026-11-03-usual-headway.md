# Usual headway and next-pass estimate at a stop

Status: draft, phase 1 (data study + proposal), 2026-11-03. Issue:
[athens-transit-map#24](https://github.com/angelospk/athens-transit-map/issues/24). No code in
the server yet. Phase 2 (build) starts only after Harold decides the points at the end.

## What Harold wants

A stop card must not mix scheduled and live rows. For each line at the stop, learn from past
passes how often a bus really passes at this hour of this kind of day ("usually every 15′, the
next hour sparser"), correct it with the schedule, and show an estimate of the next arrival
when no bus is live. Fallback: the mean gap between runs.

## Data and method

- **History**: the hourly fix files of the stopped mini PC container (`atrt_atrt-state`),
  2026-10-06 12:35 to 2026-10-29 02:37 (23 days, 540 files). 482 minutes with no fix at all
  (server outages, runs of ≥ 5 empty minutes) are masked. Static GTFS 2026-07-08 (the feed the
  rows were matched with).
- **Lines** (12; two mid-route stops each, 22 stops): dense 040, 608, 550, Χ95; medium 224,
  421, 11, 721; sparse loop lines 914, 309Β; night 500, 790.
- **Pass** = the time a vehicle's `s_m` crosses the stop's position on its shape, linear
  between two fixes ≤ 10 min apart. Two rules remove matcher jitter: a run counts only if it
  starts ≥ 1.5 km before the stop (or at the shape start), and the same bus at the same stop
  needs ≥ 0.6 × the trip time between passes (loop lines showed the same bus every ~20′).
- **Day type**: weekday, Saturday, Sunday/holiday (2026-10-28 = Sunday). Service day 04:00-04:00.
- **Queries**: every 5 min of every day at every stop (103,433). Truth = the next tracked pass
  within 3 h; a query whose wait overlaps an outage is dropped.
- **No live bus** = the bus that comes next had no fix on its run yet at query time. This is
  the case the estimate is for. Share of queries: dense 13%, medium 25%, night 60%, sparse 67%.
- **Leave-one-week-out**: a test day learns only from days of the same day type in the other
  weeks (weekday ≈ 10-14 days, Saturday/Sunday 2-3 days).

Estimators (prediction = time of the next pass):

| Id | Estimator |
|---|---|
| S | schedule only: the GTFS 2026-07-08 timetable of the same weekday (what the server has) |
| D | OASA daily schedule of 2026-11-03 (winter, Tuesday) + GTFS stop offsets, as production builds `timetable` Arrivals; weekdays only |
| C | schedule corrected: each scheduled pass + its median observed offset; dropped when it ran on < 50% of training days |
| H | history: median over training days of "the first pass after this time of day" |
| G | headway fallback: now + median observed gap (±30 min, training days) / 2 |

Error = |prediction − truth| in minutes. Scripts and full output:
[`scripts/headway-study/`](../../../scripts/headway-study/) (`results.txt`).

## Results

### 1. Is the headway stable?

Weekday, 06:00-22:00, per stop and hour: CV of the pass count from day to day, and the mean
change of that count from week to week.

| Line | Passes/h | CV day-to-day | Week-to-week change | Observed / GTFS passes |
|---|---|---|---|---|
| 608 | 7.0 | 0.31 | 1.2 /h | 1.12 |
| 040 | 5.5 | 0.30 | 0.9 /h | 0.97 |
| 550 | 4.9 | 0.31 | 1.0 /h | 0.97 |
| 224 | 4.2 | 0.30 | 0.8 /h | 0.90 |
| Χ95 | 3.9 | 0.30 | 0.6 /h | 0.88 |
| 421 | 3.3 | 0.31 | 0.7 /h | 1.07 |
| 11 | 2.2 | 0.53 | 0.7 /h | 0.74 |
| 721 | 2.3 | 0.49 | 0.8 /h | 0.92 |
| 914 | 0.8 | 0.83 | 0.4 /h | 1.17 |
| 309Β | 0.6 | 1.07 | 0.6 /h | 0.67 |

- Dense and medium lines: stable at the hour level (CV 0.3, weeks differ by ≤ 1.2 buses/h).
  Single gaps vary a lot (bunching): 040 at Νέο Φάληρο, weekday 08:00, median gap 7′, IQR
  5-10′; 17:00, 11′ (5-20′).
- Sparse loop lines and line 11 are not stable: CV 0.5-1.1. Line 11 runs about 75% of the
  GTFS trips.
- The real timetable differs from the summer GTFS: 040 at 06:00 passes every 9′, GTFS says 21′.

### 2. Does history beat "schedule only" when no bus is live?

All day types, no live bus, queries where S and H both predict (n = 21,356):

| | median | p75 | p90 | within 5′ |
|---|---|---|---|---|
| S schedule (GTFS) | 13.9′ | 41′ | 74′ | 29% |
| C schedule corrected | 12.5′ | 42′ | 81′ | 37% |
| **H history** | **11.4′** | **36′** | **73′** | **37%** |
| G headway / 2 | 25.5′ | 54′ | 98′ | 11% |

H is closer than S by more than 1 min in 53% of queries and farther in 33%.

Weekdays only, with the production timetable baseline D (n = 13,502):

| | median | p90 | within 5′ |
|---|---|---|---|
| S schedule (GTFS) | 12.4′ | 82′ | 32% |
| D OASA daily schedule | 14.8′ | 84′ | 29% |
| **H history** | **8.9′** | **82′** | **41%** |

By line class (all day types, median / within 5′): dense S 20.1′/20%, H 19.3′/30%; medium S
17.2′/25%, H 13.9′/34%; night S 6.9′/42%, H 4.9′/51%; sparse S 8.6′/36%, H 8.7′/40%.

### 3. When can we trust a time?

The spread of the training days (IQR of their "next pass" times) predicts the error well:

| Spread (IQR) | Share of no-live queries | H median | H within 5′ | S median |
|---|---|---|---|---|
| ≤ 3′ | 26% | 3.7′ | 55% | 8.6′ |
| 3-6′ | 21% | 7.9′ | 41% | 12.1′ |
| 6-10′ | 16% | 15.5′ | 28% | 19.0′ |
| > 10′ | 37% | 17.5-18.9′ | 25% | 17-19′ |

Weekdays: spread ≤ 3′ covers 21% of the no-live queries, H median 2.1′ and 69% within 5′
(D: 5.9′, 47%).

### 4. Where the error comes from

In 26-31% of the no-live queries (31% where S and H both predict) the real next bus came more than 20 min after both S and H said:
the expected run did not come (cancelled, or a bus without working telematics). No estimator
can see that; it sets the p90 of all of them at 60-100′. When the expected bus did come, H
has a median error of 4.3′ (S 6.6′).

### Answers

1. Stable enough on dense and medium lines at the hour level; not on sparse loop lines.
2. History beats schedule-only, but modestly: median 13.9′ → 11.4′ (weekdays 12.4′ → 8.9′),
   within 5′ 29% → 37% (weekdays 32% → 41%). It beats the production daily-schedule baseline
   in 54% of weekday queries and loses in 28%.
3. A real gain only where past weeks agree (spread ≤ 3′, about 1 in 4 no-live cases): median
   2-4′, 55-69% within 5′.
4. "Mean gap" as a time (now + gap / 2) is the worst (11% within 5′). Use the headway only as
   text ("every ~15′"), never as a time.
5. "Schedule corrected by history" (C) does not beat plain history (H). The schedule is best
   used as a guard (does the line run today, which day type), not as the base.

## Proposal

### Estimator

Per (line, stop, headsign, day type), from the last 28 days of history:

- `next(q)` = median over training days of the first pass after time of day `q`;
  `spread(q)` = IQR of those passes; `days(q)` = training days with a pass within 3 h.
- `headway(h)` = median gap between passes in hour `h` over training days (needs ≥ 6 gaps).
- Card rule, per line + headsign with no live Arrival:
  1. `days ≥ 3` and `spread ≤ 3′` → a time: "~21:40".
  2. else, `headway(h)` known → "usually every ~H′"; add "sparser after HH:00" when
     `headway(h+1) ≥ 1.5 × headway(h)`.
  3. else nothing.
- Guard (not measured): drop the time when today's OASA daily schedule has no run that can
  reach the stop within ±15′ of it. Holidays use the Sunday day type (the server needs a short
  holiday list, or takes the day type from the daily schedule).

### Where it runs: precomputed table in the backend

- A nightly job, next to the daily statistics job, reads the history files already on disk.
  No new OASA requests.
- It writes, per (line, stop, headsign, day type), the distinct `next(q)` values on a 5-min
  grid with their spread and days, and the hourly headways. Size: the GTFS has about 18,000
  line-stop pairs and 650k scheduled stop passes per weekday (550k Saturday, 400k Sunday), so
  about 1.6M entries × 4 bytes ≈ 7 MB, in memory and as a file in the state directory.
- `/v2/stops/{id}` serves it as a new additive field. Web and app read the same field.
- Not a static file: 8,040 stops means per-stop files or a ~7 MB download, and the app would
  need a second path. Not computed per request: that needs all raw passes of 28 days in memory.

### Contract sketch (`/v2/stops/{id}`, additive)

```json
"usual": [
  {"line": "oasa:309Β", "headsign": "ΑΥΛΑΚΙ",
   "day_type": "weekday", "days": 4,
   "headway_min": 60, "next_hour_headway_min": 90,
   "next_at": 1793742000, "spread_s": 120}
]
```

- One row per Line + headsign of the Stop, also when a bus is live (the headway is still
  useful); the client hides `next_at` when that Line + headsign has a `live` Arrival.
- `next_at` is null unless rule 1 holds (the server owns the threshold). `headway_min` and
  `next_hour_headway_min` are null when unknown.
- The web card then shows only `live` Arrivals and, for a line without one, one grey `usual`
  line. The `timetable` Arrivals stay in the contract for the app.

### Card wording (Greek, pick one)

1. Short: «Χωρίς live · συνήθως κάθε ~15′» / «Χωρίς live · επόμενο ~21:40 (εκτίμηση)».
2. Says where it comes from: «Συνήθως περνά κάθε ~15′ τέτοια ώρα» / «Συνήθως περνά ~21:40
   (από τις προηγούμενες εβδομάδες)».
3. Chip + second line: «~21:40 εκτ.» or «κάθε ~15′», under it «πιο αραιά μετά τις 22:00»;
   the ⓘ explains the estimate.

## Decisions for Harold

1. Show a time only when past weeks agree (spread ≤ 3′: about 1 in 4 no-live cases, 55-69%
   within 5′), else the headway text? Or always a time (median error 9-11′)? Recommended:
   only when they agree.
2. Wording 1, 2 or 3.
3. Backend field on `/v2/stops/{id}` (recommended) or a static file.

## Limits

- 23 days of data. Saturday and Sunday learn from only 2-3 days; their numbers are weak.
  Production keeps 30 days (4 per weekend day type).
- Data is the mini PC copy up to 2026-10-29. Production history since then (Alexandros's host)
  was not read. Re-run the scripts on it before phase 2.
- Truth = tracked passes: a bus without telematics looks like a missing bus for every
  estimator.
- S is the summer GTFS. D applies the 2026-11-03 daily schedule to October weekdays (October's
  daily schedules were not stored); the loop lines 309Β and 914 fit it badly, maybe because
  their timetable changed.
- Pass detection on loop line 309Β finds only 55-67% of the GTFS passes.
- "No live bus" here = the next bus had no fix on its run. In production, OASA's own
  predictions (contract rev 11) cover part of these cases.
