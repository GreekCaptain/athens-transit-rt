# Usual headway and next-pass estimate at a stop

Status: phase 1 (data study + proposal) 2026-11-03; Harold decided 2026-11-04; phase 2 built
(see [Phase 2](#phase-2-as-built)). Issue:
[athens-transit-map#24](https://github.com/angelospk/athens-transit-map/issues/24).

## What Harold wants

A stop card must not mix scheduled and live rows. For each line at the stop, learn from past
passes how often a bus really passes at this hour of this kind of day ("usually every 15′, the
next hour sparser"), correct it with the schedule, and show an estimate of the next arrival
when no bus is live. Fallback: the mean gap between runs.

## Data and method

- **History**: the hourly fix files of the stopped mini PC container (`atrt_atrt-state`),
  2026-10-06 12:35 to 2026-10-29 02:37 (23 days, 540 files). 482 minutes with no fix at all
  (server outages, runs of ≥ 5 empty minutes) are masked, in test and in training days.
  Static GTFS 2026-07-08 (the feed the rows were matched with).
- **Lines** (12; two mid-route stops each, 22 stops): dense 040, 608, 550, Χ95; medium 224,
  421, 11, 721; sparse loop lines 914, 309Β; night 500, 790.
- **Pass** = the time a vehicle's `s_m` first crosses the stop's position on its shape, linear
  between two fixes ≤ 10 min apart. Two rules remove matcher jitter: a run counts only if it
  starts ≥ 1.5 km before the stop (or at the shape start), and the same bus at the same stop
  needs ≥ 0.6 × the trip time between passes (loop lines showed the same bus every ~20′).
- **Day type**: weekday, Saturday, Sunday/holiday (2026-10-28 = Sunday). Service day 04:00-04:00
  in local wall-clock time (the DST change of 2026-10-25 is handled).
- **Queries**: every 5 min of every day at every stop (139,394; the repeated DST hour of 2026-10-25 is left out). Truth = the next tracked pass
  within 3 h. A query whose wait overlaps an outage is dropped. A query with no tracked pass
  within 3 h (and no outage) is kept as **censored**.
- **No live bus** = no bus that reaches the stop within 3 h was on its run at query time.
  This is the case the estimate is for. Share of queries: dense 17%, medium 30%, night 89%,
  sparse 86% (night and sparse include the hours without service).
- **Leave-one-week-out**: a test day learns only from days of the same day type in the other
  weeks (weekday ≈ 10-14 days, Saturday 2, Sunday 2-3).

Estimators (prediction = time of the next pass):

| Id | Estimator |
|---|---|
| S | schedule only: the GTFS 2026-07-08 timetable of the same weekday (what the server has) |
| D | OASA daily schedule of 2026-11-03 (winter, Tuesday) + GTFS stop offsets, as production builds `timetable` Arrivals; weekdays only |
| C | schedule corrected: each scheduled pass + its median observed offset; dropped when it ran on < 50% of training days |
| H | history: median over training days of "the first pass after this time of day" |
| G | headway fallback: now + median observed gap (±30 min, training days) / 2 |

Error = |prediction − truth| in minutes. Unless stated, each table compares estimators on the
same queries (all of them predict). Scripts and full output:
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
| 721 | 2.3 | 0.47 | 0.8 /h | 0.90 |
| 914 | 0.8 | 0.83 | 0.4 /h | 1.17 |
| 309Β | 0.6 | 1.07 | 0.6 /h | 0.67 |

- Dense and medium lines: stable at the hour level (CV 0.3, weeks differ by ≤ 1.2 buses/h).
  Single gaps vary a lot (bunching): 040 at Νέο Φάληρο, weekday 08:00, median gap 7′, IQR
  5-10′; 17:00, 11′ (5-20′).
- Sparse loop lines and line 11 are not stable: CV 0.5-1.1. Line 11 runs about 75% of the
  GTFS trips.
- The real timetable differs from the summer GTFS: 040 at 06:00 passes every 9′, GTFS says 21′.

### 2. Coverage: the censored queries

60% of the no-live queries have no tracked pass within 3 h: mostly hours without service
(night lines by day, sparse lines in the evening), also buses without telematics. In these
queries S still predicts a bus in 21% and H in 18%: a false "next ~HH:MM". 57% of the
training-day lookups also find no pass. The error tables below cover only queries with a
tracked pass.

### 3. Does history beat "schedule only" when no bus is live?

No live bus, queries where S, C and H all predict (n = 18,970):

| | median | p75 | p90 | within 5′ |
|---|---|---|---|---|
| S schedule (GTFS) | 13.6′ | 42′ | 79′ | 31% |
| C schedule corrected | 12.3′ | 45′ | 84′ | 40% |
| **H history** | **10.8′** | **37′** | **78′** | **39%** |

H is closer than S by more than 1 min in 53% of queries and farther in 32%. C and H are about
equal; C is better on night lines (median 3.6′ vs 3.9′), H has the shorter tail on sparse lines
(p90 59′ vs 79′).

Headway fallback, on the queries where S, H and G all predict (n = 12,327): S 23.8′ / 17%
within 5′, H 22.1′ / 26%, **G 28.9′ / 10%**.

Weekdays only, with the production timetable baseline D (n = 12,668):

| | median | p90 | within 5′ |
|---|---|---|---|
| S schedule (GTFS) | 12.6′ | 86′ | 33% |
| D OASA daily schedule | 15.4′ | 90′ | 28% |
| **H history** | **9.0′** | **85′** | **41%** |

H is closer than D in 54% of these queries and farther in 29%.

By line class (median / within 5′): dense S 21.7′/20%, H 22.9′/30%; medium S 18.3′/25%, H
14.9′/36%; night S 5.5′/49%, H 3.9′/58%; sparse S 8.3′/39%, H 7.5′/43%.

### 4. When can we trust a time?

With the proposed gate (≥ 3 training days, which keeps 85% of the queries above), the spread of
the training days (IQR of their "next pass" times) predicts the error:

| Spread (IQR) | Share of queries | H median | H p90 | H within 5′ | S median | S within 5′ |
|---|---|---|---|---|---|---|
| ≤ 3′ | 21% | 2.6′ | 59′ | 64% | 5.5′ | 48% |
| 3-6′ | 19% | 7.5′ | 88′ | 43% | 11.1′ | 35% |
| 6-10′ | 12% | 21.0′ | 113′ | 30% | 24.7′ | 23% |
| > 10′ | 32% | 19.9′ | 83′ | 27% | 21.6′ | 21% |

The rule "≥ 3 days and spread ≤ 3′" fires on 22% of the weekday queries above (H median 2.0′,
p90 47′, 69% within 5′; S median 4.3′) and on 30% of the Sunday ones (H 4.2′, p90 66′, 51%;
S 8.9′). Counted over all no-live queries, censored ones included, it fires on 15% (weekday)
and 12% (Sunday). Of those, no bus came within 3 h in 23% (weekday) and 6% (Sunday): a time
shown for a bus that never came. It never fires on Saturdays here: only 2 training days. Even
with a bus, about 1 in 10 waits is ~an hour longer than the estimate.

### 5. The long tail

In 26-32% of the no-live queries the next tracked pass came more than 20 min after both S and
H. This is selected on the outcome, so it does not prove a cause; likely causes are a run that
did not run, or a bus without working telematics. These queries set the p90 of every estimator
at 60-110′.

### Answers

1. Stable enough on dense and medium lines at the hour level; not on sparse loop lines.
2. History beats schedule-only, but modestly: median 13.6′ → 10.8′ (weekdays 12.6′ → 9.0′),
   within 5′ 31% → 39% (weekdays 33% → 41%). It beats the production daily-schedule baseline
   in 54% of weekday queries and loses in 29%.
3. A real gain only where past weeks agree (≥ 3 days, spread ≤ 3′; 12-15% of all no-live
   queries): median 2-4′, 51-69% within 5′ when a bus came. The tail stays (p90 47-66′), and on
   weekdays 23% of these times point at a bus that did not come within 3 h.
4. "Mean gap" as a time (now + gap / 2) is the worst (10% within 5′). Use the headway only as
   text ("every ~15′"), never as a time.
5. "Schedule corrected by history" (C) is about as good as plain history (H). H needs no
   timetable, so it also works when the GTFS is expired or wrong. Use the schedule as a guard
   (does the line run today, which day type).
6. Without a guard, an estimate also appears when no bus comes at all (18-21% of the censored
   queries). The guard below must cut these.

## Proposal

### Estimator

Per (line, stop, headsign, day type), from the last 28 days of history (outage hours left out):

- `next(q)` = median over training days of the first pass after time of day `q`;
  `spread(q)` = IQR of those passes; `days(q)` = training days with a pass within 3 h.
- `headway(h)` = median gap between passes in hour `h` over training days (needs ≥ 6 gaps).
- Card rule, per line + headsign with no live Arrival:
  1. `days ≥ 3`, `spread ≤ 3′`, and most training days had a pass within 3 h (not measured) → a time:
     "~21:40, estimate".
  2. else, `headway(h)` known → "usually every ~H′"; add "sparser after HH:00" when
     `headway(h+1) ≥ 1.5 × headway(h)`.
  3. else nothing.
- Guard (not measured): drop the time when today's OASA daily schedule has no run that can
  reach the stop within ±15′ of it. Holidays use the Sunday day type (the server needs a short
  holiday list, or takes the day type from the daily schedule).

### Where it runs: precomputed table in the backend

- A nightly job, next to the daily statistics job, reads the history files already on disk.
  No new OASA requests.
- It writes, per (line, stop, headsign, day type), `next(q)` on a 5-min grid; consecutive grid
  points with the same (next, spread, days) merge into one row (start minute uint16 + next
  minute uint16 + spread uint8 + days uint8 = 6 B), plus the hourly headways.
- Size, measured on the study stops with one fit on all days: 2.26 rows per scheduled pass.
  The GTFS has 1.61M scheduled stop passes over the three day types, so about 3.6M rows ≈
  22 MB, plus 2.6 MB of hourly headways. That is too much to hold next to the rest under
  `mem_limit: 200m`. Keep it as a file in the state directory, sorted by stop, with an index;
  `/v2/stops/{id}` reads one stop's slice (about 3 KB on average).
- `/v2/stops/{id}` serves it as a new additive field. Web and app read the same field.
- Not a static file for the web: ~25 MB, or 8,040 per-stop files, and the app would need a second
  path. Not computed per request from raw passes: that needs all passes of 28 days.

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

1. Show a time only when past weeks agree (≥ 3 days, spread ≤ 3′: 12-15% of no-live cases,
   51-69% within 5′ when a bus came; but on weekdays 23% of them no bus came, and 1 in 10 is
   ~an hour off), else the headway text? Or always a time (median error 9-11′, p90 ~80′)?
   Recommended: only when they agree, worded as an estimate, with the schedule guard.
2. Wording 1, 2 or 3.
3. Backend field on `/v2/stops/{id}` from a ~25 MB disk table (recommended), or a static file.

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
- Pass detection on loop line 309Β finds only 52-54% of the GTFS passes (night lines 500 and 790 see 1.2-1.4×: the summer GTFS has fewer night runs than OASA runs).
- "Live" sees only buses with a recorded crossing of this stop within 3 h. A live bus whose
  crossing was not detected counts as no-live (a small share; passes found are within about ±15%
  of the GTFS count on most lines).
- In production, OASA's own predictions (contract rev 11) cover part of the no-live cases.

## Phase 2 (as built)

Harold's decisions (2026-11-04):

1. A time only when the past weeks agree (≥ 3 days, spread ≤ 3′) and the schedule guard
   confirms a run; else «Συνήθως περνά κάθε ~H′ τέτοια ώρα».
2. Wording 2: «Συνήθως περνά κάθε ~15′ τέτοια ώρα» / «Συνήθως περνά ~21:40 (από τις
   προηγούμενες εβδομάδες)».
3. A backend field `usual` on `/v2/stops/{id}` (contract rev 18), built nightly from the
   history, no OASA load.

What changed from the proposal:

- **Day files of passes, not a 5-min grid.** The nightly job (`internal/usual`) writes one
  immutable file per finished service day: the stop passes (stop, line, headsign, time) and the
  outage minutes, sorted by stop, with an index. `/v2/stops/{id}` reads that stop's slice of
  each training day and computes `next`, `spread` and the headways for the exact time of the
  request. A new day costs one build; a GTFS change does not rebuild old days. Measured on the
  23 days of the study: 0.2-0.8 s per day, 0.5-1.3 MB per file (≈ 35 MB for 28 days), +31 MB
  heap at peak while one day builds (the feed is already loaded), 6 MB for the 23 indexes, one
  query ≈ 0.1 ms.
- **Pass detection** is the study's `passes.py` ported to Go, with the matcher's stop snapping
  (`geo.SnapStops`, so it agrees with `s_m`). On the 22 study stops it finds 99.4% of the
  study's passes within 60 s (309Β: 90%, a loop line where the two snappings differ).
- **"Most days" rule** (proposal rule 1, not measured): `next_at` also needs more than half of
  the training days that could be read (no outage over the wait) to have a pass within 3 h.
- **Guard**: an Arrival of the same line and headsign (today's timetable or a bus) within
  ±15 min of `next_at`. The board lists Arrivals up to 2 h ahead, so a `next_at` further away
  is dropped.
- **Holidays**: fixed Greek public holidays plus Clean Monday, Easter Monday and Whit Monday
  count as Sunday. Good Friday and Holy Saturday keep their weekday type.

Not done: the study was not re-run on the production history (the production host is not
reachable from the dev machines). Once deployed, the server's log line `usual passes built`
gives the per-day pass counts there.

