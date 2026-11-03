# Trip planner `/v2/plan` (issue #44)

Status: proposed (phase 1: contract + design). The client contract is
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

- A rebuild makes a new timetable and swaps it with `atomic.Pointer`: the peak is 2 × steady,
  ≈ 20 MB, for a few seconds once a day.
- Per query: labels for K = 5 rounds × 8.2k stops (arrival int32 + back pointer 8 B) ≈ 0.5 MB,
  plus the marked-stop bitsets. Taken from a `sync.Pool`.
- At most 2 plans run at once (a semaphore); a request waits up to 2 s for a slot, then gets
  `429 busy`. So the worst query memory is ≈ 1 MB.
- Acceptance (phase 2, on the mini PC, weekday timetable): steady heap growth ≤ 20 MB, build
  ≤ 2 s, `last=true` p95 ≤ 150 ms, `last=false` p95 ≤ 100 ms. If the heap is over budget,
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

Times are seconds after midnight of the service day (past 24 h after midnight). The three
adapters in `internal/server` fill the input from what they already hold:

- `oasa`: GTFS trips active on the day while the GTFS is current; else `dailyRuns` of every
  line with a cached daily schedule (`dailyCached`; no fetch). Run ids as §3.
- `stasy`: the timetable's trips of the day (same-weekday stand-in when expired).
- `proastiakos`: the cached day board's runs (`board` with the existing 10-min cache; the
  planner never forces a fetch).

Steps:

1. **Routes.** Group trips by their stop sequence. Sort each group by the departure at the
   first stop. RAPTOR needs trips that never overtake one another on a route (FIFO). On an
   expired GTFS two daily runs can take stop offsets from different GTFS trips, so a later run
   can arrive first. Each group is split greedily: a trip goes to the first sub-route whose last
   trip it does not overtake at any stop, else to a new sub-route.
2. **Transfers.** A grid of ~500 m cells; for each stop, the stops within 500 m in its cell and
   the 8 neighbours. Time = ceil(distance × 1.3 / 1.2) + 60 s (+120 s per metro or suburban end).
   A stop to itself: 60 s (change at the same stop). Stored as CSR.
3. **Indices**: routes per stop (CSR), the grid for access and egress.

When: at start once the Sources have loaded; then after 03:00 when the prefetch round of the
new day ends (and at 04:30 in any case, with what is cached); and when the STASY feed or the
suburban board of the day changes. A build that fails keeps the old timetable. The planner
serves the service day of its timetable; after 03:00 and before the new build, a request gets
the new `service_day` only once the build is done (until then the old day, whose late runs are
over, gives few or no options; the response says which `service_day` it used).

## Search

Access: the stops within 1000 m of `from` (at most the nearest 60), each with its walk time
(+120 s at a metro or suburban stop). Egress: the same around `to`.

- `last=false`: rRAPTOR over the departure times at the access stops in [`after`, `after` +
  3 h], from the latest down. Each RAPTOR run starts from the access stops at time τ + walk;
  the labels of the previous (later) run stay as upper bounds. A journey is kept when no
  journey that leaves later arrives at `to` no later with no more transfers. At the end the
  `n` journeys that leave first are returned.
- `last=true`: the same, but the departure times run from the end of the service day's
  timetable down to `after`, and the search stops once `n` journeys are kept.
- Rounds: K = 4 rides. Target pruning: a label later than the best arrival at `to` for that
  round is dropped.
- Ties: equal (departure, arrival, transfers): less walking wins, then fewer hops.
- The journey is rebuilt from the back pointers (route, trip, board stop, alight stop, or a
  transfer), then turned into contract legs. `lines` and `stops` come from the adapters' Line
  and Stop objects.

## Response shape decisions

- Legs alternate walk / ride, walks at both ends always present (0 m when on a Stop): one
  client rule, no special cases.
- `run` on every ride: the client opens `/v2/runs/{id}` for the live state; the plan itself
  stays timetable-only and cacheable for 60 s.
- `lines` and `stops` maps: the card renders without `/v2/stops/{id}` calls.
- `walk_only` beside the list, not in it: a walk is always possible and would push the rides
  out of a list of `n`.

## Limits and their reasons

| Limit | Value | Reason |
|---|---|---|
| Access / egress walk | 1000 m straight line | Evening walk home; beyond it a taxi; keeps the access set ≤ 60 stops |
| Transfer walk | 500 m | Issue #44; bounds the transfer table (memory) |
| Walk speed | 1.2 m/s, straight line × 1.3 | Common planner default; Athens streets are not a grid |
| Transfer buffer | 60 s; +120 s at metro and suburban | Stop coordinates are platform means; stations have stairs |
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
6. Runs after midnight (times > 24 h) and an `after` at 01:30 on the previous service day.
7. FIFO split: an overtaking trip on the same stop sequence is still found.
8. No route: empty list; points 800 m apart: `walk_only` set.
9. Access limit: a stop 1100 m away is not used.

Handler tests: parameter errors, `429`, cache headers, `sources` states, `warming_up` on an
expired GTFS with no daily cache. A fixture `docs/fixtures/v2/plan.json` from a test feed.

Real queries checked by hand against the timetables (in the PR): Σύνταγμα → Γλυφάδα after
23:30 (`last=true`), Πειραιάς → Κηφισιά, Χαλάνδρι → Περιστέρι (bus + metro), and a night-line
trip after 01:00.

Benchmark: `BenchmarkBuild` and `BenchmarkPlan` on the GTFS snapshot plus the test STASY feed,
heap measured with `runtime.ReadMemStats` before and after the build.
