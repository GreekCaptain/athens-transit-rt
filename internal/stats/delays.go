package stats

import (
	"math"
	"sort"
)

// LineDelay is how late one line's runs of OASA's daily schedule left their first stop on a day:
// runs measured, mean and p90 (nearest rank) in seconds, the sum (so days add up), and a
// histogram of whole minutes ([minute, runs], minute = floor(delay / 60), by minute) so a p90 of
// several days can be read from the days' histograms.
type LineDelay struct {
	Line    string   `json:"line"`
	Runs    int      `json:"runs"`
	MeanS   int      `json:"mean_s"`
	P90S    int      `json:"p90_s"`
	SumS    int64    `json:"sum_s"`
	Minutes [][2]int `json:"minutes"`
}

// StartDelays are the start delays of a day's runs (from the history's run_start and
// start_delay_s, written by the server for a bus seen leaving on a clear run; rt#55).
type StartDelays struct {
	Runs   int         `json:"runs"`
	ByLine []LineDelay `json:"by_line"`
}

type runKey struct {
	line, veh string
	run       int64
}

// delayCounter keeps one start delay per run (line, vehicle and scheduled departure) that
// departs in [from, to): the first one read.
type delayCounter struct {
	from, to int64
	runs     map[runKey]int
}

func newDelayCounter(from, to int64) *delayCounter {
	return &delayCounter{from: from, to: to, runs: map[runKey]int{}}
}

func (c *delayCounter) add(line, veh string, run int64, delay int) {
	if run < c.from || run >= c.to {
		return
	}
	k := runKey{line, veh, run}
	if _, ok := c.runs[k]; !ok {
		c.runs[k] = delay
	}
}

// result is nil when no run was measured.
func (c *delayCounter) result() *StartDelays {
	if len(c.runs) == 0 {
		return nil
	}
	byLine := map[string][]int{}
	for k, d := range c.runs {
		byLine[k.line] = append(byLine[k.line], d)
	}
	out := &StartDelays{Runs: len(c.runs), ByLine: make([]LineDelay, 0, len(byLine))}
	for line, ds := range byLine {
		sort.Ints(ds)
		l := LineDelay{Line: line, Runs: len(ds), Minutes: [][2]int{}}
		for _, d := range ds {
			l.SumS += int64(d)
			m := int(math.Floor(float64(d) / 60))
			if n := len(l.Minutes); n > 0 && l.Minutes[n-1][0] == m {
				l.Minutes[n-1][1]++
			} else {
				l.Minutes = append(l.Minutes, [2]int{m, 1})
			}
		}
		l.MeanS = int(math.Round(float64(l.SumS) / float64(len(ds))))
		l.P90S = ds[int(math.Ceil(0.9*float64(len(ds))))-1]
		out.ByLine = append(out.ByLine, l)
	}
	sort.Slice(out.ByLine, func(i, j int) bool { return out.ByLine[i].Line < out.ByLine[j].Line })
	return out
}
