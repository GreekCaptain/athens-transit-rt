# Headway study (athens-transit-map#24, phase 1)

One-off analysis behind
[`docs/superpowers/specs/2026-11-03-usual-headway.md`](../../docs/superpowers/specs/2026-11-03-usual-headway.md).
Not part of the server. Python 3 + pandas + pyarrow.

Run in an empty work directory (all paths are relative to it):

1. `gtfs/` = the unzipped `osy_gtfs.zip` (version 2026-07-08); `hist/` = the hourly history
   files (`history/*.csv.gz` from a `--history` state directory).
2. `prep_gtfs.py` then `prep_hist.py` (the 12 study lines only).
3. `passes.py`: stop passes = the time a run's `s_m` crosses the stop's position on its shape.
4. `pick.py`: two mid-route stops per line. `eval.py`: leave-one-week-out predictions.
5. `final.py`, `decomp.py` and `daily.py` (needs `daily/<line>.json` = `/v1/lines/{line}/schedule` of a
   weekday) print the tables in `results.txt`.
