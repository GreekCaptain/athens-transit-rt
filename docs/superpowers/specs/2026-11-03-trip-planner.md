# Trip planner `/v2/plan` (issue #44)

Status: implemented (phase 2, PR #64; measured numbers at the end). The client contract is
[CONTRACT.md §9](../../CONTRACT.md#9-trip-planner-v2plan-rev-15); this note is how the backend
meets it and what it costs.

## Goal

The app's evening section «Τελευταία για σπίτι»: from where the user is to a saved place, the
last trips of the day, with transfers (metro + bus, bus only, night bus) and the walks before,
between and after. Timetable only in this step. Out of scope: the "risky" mark (needs 2-3 weeks
of seen/missed stats), live delays, fares, `arrive_by`.

## Why RAPTOR in process

- OpenTripPlanner needs about 1-2 GB of heap for Attica. The service runs with `mem_limit: 200m`
  and `GOMEMLIMIT=100MiB` (README, Self-hosting). Not possible.
- RAPTOR (Delling, Pajor, Werneck 2012) works on flat arrays of stop times, has no graph
  preprocessing, gives Pareto options over (arrival, transfers) in one run, and its range
  version (rRAPTOR) answers "the last options of the day" by running it for the departure times
  in descending order, reusing the labels. It fits a few MB.
- No new dependency. All code is new, in `internal/plan`, with no imports from `internal/server`.

## Sizes (measured 2026-11-03)

| Data | Count | Source |
|---|---|---|
| Stops, all Modes | 8,211 (bus 7,820, trolley 710, metro 66, tram 60, suburban 45) | live `/v2/stops` |
| OASA trips, a weekday | 15,097 trips, 656,767 stop events | GTFS snapshot 2026-07-08, 2026-09-15 |
| OASA trips, Saturday / Sunday | 12,526 / 548,515; 9,243 / 406,352 | same |
| OASA stop patterns | 509 | same |
| STASY trips, a weekday | ~4,000 trips, ~80,000 events (estimate, phase 2 measures) | STASY GTFS |
| Suburban trains | ~200 trains, ~3,000 events (estimate) | day board |

On an expired GTFS the bus runs come from OASA's daily schedule (one departure per run); each run
takes the stop offsets of its GTFS stand-in trip (`v2_daily.go`), so the event count is the same
order.

## Memory budget

Steady (one service day), estimates that the phase 2 benchmark must confirm:

| Part | Size |
|---|---|
| Stop events: `arr`, `dep` int32 per (trip, stop), contiguous per route | ~740k × 8 B ≈ 6 MB |
| Trips: route, run id index, first-stop time | ~20k × 16 B ≈ 0.3 MB |
| Run ids, built only for the trips a response returns | ~0 |
| Routes: stop lists, route-per-stop index (CSR) | < 0.5 MB |
| Transfers ≤ 500 m (CSR: target stop int32 + seconds uint16) | ~8.2k × ~60 × 6 B ≈ 3 MB |
| Stop grid for the 1000 m access search (cells of ~500 m) | < 0.3 MB |
| **Total** | **≈ 10 MB** |

- A rebuild makes a new timetable and swaps it with `atomic.Pointer`. During the build the old
  timetable, the `Input` (one struct per trip with its stop times: ~740k × 12 B ≈ 9 MB plus
  ids) and the build scratch (grouping maps, the transfer grid) live together: estimate
  ≈ 30 MB peak for a few seconds. The adapters may hand the input route by route to cut it.
- Per query: labels for K = 5 rounds × 8.2k stops (arrival int32 + back pointer 8 B) ≈ 0.5 MB,
  plus the marked-stop bitsets. Taken from a `sync.Pool`.
- At most 2 plans run at once (a semaphore); a request waits up to 2 s for a slot, then gets
  `429 busy`. So the worst query memory is ≈ 1 MB.
- Acceptance (phase 2, on the mini PC, weekday timetable): steady heap growth ≤ 20 MB; peak
  heap growth ≤ 40 MB, sampled (`runtime/metrics`, every 10 ms) during a build with 2 queries
  running; build ≤ 2 s; `last=true` p95 ≤ 150 ms, `last=false` p95 ≤ 100 ms. Heap growth does
  not prove the service fits the container, so the same run is also measured whole: the
  service image with the real state, live polling on, a rebuild forced while 2 plans run, and
  the cgroup's `memory.peak` (or `docker stats` max) must stay ≤ 160 MB (80 % of
  `mem_limit: 200m`), with the baseline before the change written next to it. If over budget,
  the first cut is the transfers (lower the radius to 400 m), then int16 times relative to the
  route's first departure.

## Build

`plan.Build(input) *Timetable`, where the input is Mode-neutral:

```go
type Input struct {
    Day   string            // service day, 2026-11-03
    Stops []Stop            // id, lat, lon, mode
    Trips []Trip            // line id, run id, mode, headsign, []StopTime{stop, arr, dep}
}
```

Times are seconds from the service day's GTFS reference ("noon minus 12 h" of day D, as
`gtfs.ServiceDay`), so they pass 24 h after midnight and stay right on DST days. The planner's
window is [D 03:00, D+1 03:00) by first departure, but the native calendars differ: OASA's GTFS
and STASY use calendar service dates with times past 24 h, the suburban boards are per calendar
date. So each adapter takes the native dates that can have a run starting in the window,
turns each native time into an instant (the native date's own reference, "noon minus 12 h" of
that date, plus the seconds), then into seconds from D's reference. A plain shift by whole
days would be wrong on DST nights, when two references are 82,800 or 90,000 s apart. The
adapter keeps the trips whose first departure is in the window. Each trip keeps its native date in its Run id (§3).

- `oasa`: while the GTFS is current, trips active on D (all their times) and on D+1 (only those
  starting before 03:00). On an expired GTFS, `dailyRuns` of every line
  with a cached daily schedule of D (`dailyCached`, never a fetch): OASA's daily schedule
  already counts 03:00 to 03:00, with small-hours runs as "24:15".
- `stasy`: trips of the timetable dates of D and D+1 (`timetableDate`, same-weekday stand-in
  when expired), shifted and filtered as above.
- `proastiakos`: the boards of D and D+1 only when already cached, through a new accessor
  `CachedBoard(date)` that reads `cachedBoard` / `staleBoard` and never fetches (the existing
  `board` may fetch, so the planner must not call it).

Steps:

1. **Routes.** Group trips by their stop sequence. Sort each group by the departure at the
   first stop. RAPTOR needs trips that never overtake one another on a route (FIFO). On an
   expired GTFS two daily runs can take stop offsets from different GTFS trips, so a later run
   can arrive first. Each group is split greedily: a trip goes to the first sub-route whose last
   trip it does not overtake at any stop, else to a new sub-route.
2. **Transfers.** A grid of ~500 m cells; for each stop, the stops within 500 m in its cell and
   the 8 neighbours. Time = ceil(distance × 1.3 / 1.2) + 60 s (+120 s per metro or suburban end).
   A stop to itself: 60 s, or 180 s at a metro or suburban stop (60 s + 120 s once). Stored as
   CSR.
3. **Indices**: routes per stop (CSR), the grid for access and egress.

When (all local events; a build never calls an upstream): at start once the Sources have
loaded; after 03:00 when the prefetch round of the new day ends (and at 04:30 in any case,
with what is cached); and, at most once per 10 min, when any input changed: a new OASA GTFS
snapshot, a line's daily schedule cached after the round (an on-demand retry of a failed
line), a new STASY feed, a suburban board of D or D+1 newly cached. A build that fails keeps
the old timetable. The planner
serves the service day of its timetable; after 03:00 and before the new build, a request gets
the new `service_day` only once the build is done (until then the old day, whose late runs are
over, gives few or no options; the response says which `service_day` it used).

## Search

Access: every stop within 1000 m of `from`, each with its walk time (+120 s at a metro or
suburban stop); about 150 in the centre, which RAPTOR seeds in one pass. Egress: the same
around `to`.

- Departure times: for each access stop s with walk w(s) and each trip departure d at s,
  the origin time is τ = d − w(s). Only τ ≥ `after` is used (the filter is on τ, not on d:
  a 10:00 departure 10 min away is reachable from `after` = 09:50).
- `last=false`: rRAPTOR over the distinct τ in [`after`, `after` + 3 h], from the latest
  down. Each RAPTOR run starts from the access stops at time τ + w(s);
  the labels of the previous (later) run stay as upper bounds. A journey is kept when no
  journey that leaves later arrives at `to` no later with no more transfers. At the end the
  `n` journeys that leave first are returned.
- `last=true`: the same, but τ runs from the latest one of the service day down to `after`.
  The search stops after the τ at which `n` journeys are kept: that τ is finished first and
  all its journeys go through the dominance rule. Then the `n` journeys that leave latest are
  picked (ties: earlier arrival, then fewer transfers), and only then sorted by `depart_at`
  for the response. Sorting ascending before the cut would drop the latest ones.
- Rounds: K = 4 rides. Target pruning: a label later than the best arrival at `to` for that
  round is dropped.
- Ties: plain RAPTOR keeps one arrival label per stop and round, so at equal (departure,
  arrival, transfers) the returned journey is the first one found; the contract does not
  promise the one with less walking. (A walking criterion would need McRAPTOR bags: more
  memory and time for little gain on the evening question.)
- Run ids: OASA daily runs of D stay valid for `/v2/runs/{id}` until 03:00 of D+1. Today
  `dailyRun` (`internal/server/v2_oasa.go`) checks the id's date against the calendar day and
  reads the daily cache of `Midnight(now)`. The cache entry of D is not safe after midnight:
  `/v1/lines/{id}/schedule` (`dailyVia`, `internal/server/schedule.go`) replaces a line's
  entry with D+1 when someone asks then. So the planner's timetable is the copy that counts: it
  holds D's daily runs (line, stand-in trip, departure) until its next build, and phase 2
  makes `dailyRun` answer an id dated D between 00:00 and 03:00 of D+1 from the planner's
  timetable, with D's times.
- The journey is rebuilt from the back pointers (route, trip, board stop, alight stop, or a
  transfer), then turned into contract legs. `lines` and `stops` come from the adapters' Line
  and Stop objects.

## Response shape decisions

- Legs alternate walk / ride, walks at both ends always present (0 m when on a Stop): one
  client rule, no special cases.
- `run` on every ride: the client opens `/v2/runs/{id}` for the live state; the plan itself
  stays timetable-only and cacheable for 60 s.
- `lines` and `stops` maps: the card renders without `/v2/stops/{id}` calls.
- `shape` on every leg, for the app's route map (Harold, 2026-11-03: map route mode, the
  evening card with "leave in X", Plan B and "leave now" alerts in the first app version).
  Rides cut the Pattern shape the backend already serves; walks are straight lines in rev 15.
  Street paths for walks later: OpenRouteService for the chosen itinerary only, cached
  (sends the user's points to a third party: needs Harold's yes); no router in the container.
- `walk_only` beside the list, not in it: a walk is always possible and would push the rides
  out of a list of `n`.

## Limits and their reasons

| Limit | Value | Reason |
|---|---|---|
| Access / egress walk | 1000 m straight line | Evening walk home; beyond it a taxi; ~150 stops in the centre |
| Transfer walk | 500 m | Issue #44; bounds the transfer table (memory) |
| Walk speed | 1.2 m/s, straight line × 1.3 | Common planner default; Athens streets are not a grid |
| Transfer buffer | 60 s; +120 s per metro or suburban end (once for a change inside one station) | Stop coordinates are platform means; stations have stairs |
| Rides | ≤ 4 | Few real trips need more; caps the rounds |
| `n` | 1-5, default 3 | One screen of options |
| `last=false` window | 3 h | Bounds the rRAPTOR runs |
| Concurrency | 2 plans, 2 s wait, then `429` | The box also serves the live map |

## Load on the upstreams

None added. The planner reads the GTFS, the daily schedules the prefetch already fetches once a
day, the STASY timetable and the suburban board that `/v2` already loads. A plan request never
calls OASA, STASY or railway.gov.gr.

## Tests (phase 2)

Golden cases on hand-made timetables (`internal/plan`, table-driven, each a few stops and
trips):

1. One direct ride, walks at both ends.
2. One transfer with a 300 m walk; a 600 m transfer is not taken.
3. The 60 s buffer: a connection with 30 s of slack is missed.
4. Pareto: a fast 2-transfer option and a slow direct one both returned.
5. `last=true` returns the latest `n`, sorted; `last=false` the first `n`.
6. Runs after midnight (times > 24 h) and an `after` at 01:30 on the previous service day;
   a STASY trip of D+1 that starts at 02:30 is in; a ride whose stop departure is 10 min
   after `after` but whose stop is 10 min away is found (τ filter).
10. Same-stop changes: 60 s at a bus stop, 180 s inside a metro station.
11. A plan's OASA daily Run id opens with `/v2/runs/{id}` at 01:30 of the next calendar day.
7. FIFO split: an overtaking trip on the same stop sequence is still found.
8. No route: empty list; points 800 m apart: `walk_only` set.
9. Access limit: a stop 1100 m away is not used.
12. Leg `shape`: a ride's shape starts and ends at its board and alight stops; a loop line
    whose shape passes the board stop twice is cut at the right pass; a walk is 2 points.

Handler tests: parameter errors, `429`, cache headers, `sources` states, `warming_up` on an
expired GTFS with no daily cache. A fixture `docs/fixtures/v2/plan.json` from a test feed.

Real queries checked by hand against the timetables (in the PR): Σύνταγμα → Γλυφάδα after
23:30 (`last=true`), Πειραιάς → Κηφισιά, Χαλάνδρι → Περιστέρι (bus + metro), and a night-line
trip after 01:00.

Benchmark: `BenchmarkBuild` and `BenchmarkPlan` on the GTFS snapshot plus the test STASY feed,
heap measured with `runtime.ReadMemStats` before and after the build.

## Measured (phase 2, 2026-11-03)

On the mini PC, GTFS snapshot 2026-07-08 on Tue 2026-09-15 (inside its validity) plus the live
STASY feed of the service's volume; no suburban board (none cached in the volume). Reproduce
with `ATRT_PLAN_STATE=<state dir> go test -run TestPlanRealData -v ./internal/server/` and the
benchmarks `BenchmarkPlanBuild`, `BenchmarkPlan`.

| Check | Acceptance | Measured |
|---|---|---|
| Timetable | — | 8,211 stops, 16,667 trips, 464 routes, 692,476 stop events, 114,550 transfers |
| Build | ≤ 2 s | 110 ms (first), 44 ms per build in the benchmark |
| Steady heap growth | ≤ 20 MB | 13.5 MB |
| Peak heap growth, a rebuild with 2 plans running (sampled every 10 ms) | ≤ 40 MB | 38 MB at `GOGC=25`; 61 MB at `GOGC=100`, garbage not yet collected included |
| `last=false` latency, 300 random pairs | p95 ≤ 100 ms | p50 15 ms, p95 45 ms, max 61 ms |
| `last=true` latency, 300 random pairs | p95 ≤ 150 ms | p50 1.4 ms, p95 7.5 ms, max 83 ms |
| cgroup `memory.peak`, `MemoryMax=200M`, `GOMEMLIMIT=100MiB` | ≤ 160 MB | 137.9 MB with the planner (3 rebuilds while 2 plans ran, 902 plans in 20 s); 102.7 MB baseline, same state, no planner |

The container check ran the service's code in a systemd scope (`TestPlanCgroupPeak`), not the
image: live polling was off, because a second poller would add load on OASA, and the live GTFS is
expired (on today's date the bus part needs daily schedules that only a polling instance caches).
Live polling adds the same memory to both numbers; check `docker stats` on the production host
after the deploy.

Where the code differs from the text above:

- Run ids are stored as strings for every trip (about 1 MB), not built on demand: simpler, and
  `/v2/runs/{id}` looks them up after midnight.
- A new service day is built at once after 03:00 when the OASA GTFS covers it (the daily
  schedules are not needed then); on an expired GTFS it waits for the prefetch round or 04:30.
- Suburban Runs of a plan also open after midnight from the planner's timetable (the board is per
  calendar day), as contract §9 promises for every Run.
- A rebuild for changed inputs waits 10 min, except when the STASY timetable or a new GTFS
  appears (a build at start often runs before STASY has loaded).
- No rebuild of the same service day after its calendar midnight: the daily cache and the
  suburban board move on to the next calendar day then, and a rebuild would drop the night's
  runs. A daily schedule that is lost (a failed retry) is no new input either; one completed
  (partial to full) is.
