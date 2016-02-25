package main

import (
	"github.com/raintank/raintank-metric/metric_tank/consolidation"
	"math"
	"testing"
)

type testCase struct {
	in     []Point
	consol consolidation.Consolidator
	num    uint32
	out    []Point
}

func validate(cases []testCase, t *testing.T) {
	for i, c := range cases {
		out := consolidate(c.in, c.num, c.consol)
		if len(out) != len(c.out) {
			t.Fatalf("output for testcase %d mismatch: expected: %v, got: %v", i, c.out, out)

		} else {
			for j, p := range out {
				if p.Val != c.out[j].Val || p.Ts != c.out[j].Ts {
					t.Fatalf("output for testcase %d mismatch at point %d: expected: %v, got: %v", i, j, c.out[j], out[j])
				}
			}
		}
	}
}

func TestOddConsolidationAlignments(t *testing.T) {
	cases := []testCase{
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Avg,
			1,
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Avg,
			3,
			[]Point{
				{2, 1449178151},
				{4, 1449178181}, // see comment below
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
			},
			consolidation.Avg,
			1,
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
			},
			consolidation.Avg,
			2,
			[]Point{
				{1.5, 1449178141},
				{3, 1449178161}, // note: we choose the next ts here for even spacing (important for further processing/parsing/handing off), even though that point is missing
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
			},
			consolidation.Avg,
			3,
			[]Point{
				{2, 1449178151},
			},
		},
	}
	validate(cases, t)
}
func TestConsolidationFunctions(t *testing.T) {
	cases := []testCase{
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Avg,
			2,
			[]Point{
				{1.5, 1449178141},
				{3.5, 1449178161},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Cnt,
			2,
			[]Point{
				{2, 1449178141},
				{2, 1449178161},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Min,
			2,
			[]Point{
				{1, 1449178141},
				{3, 1449178161},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Max,
			2,
			[]Point{
				{2, 1449178141},
				{4, 1449178161},
			},
		},
		{
			[]Point{
				{1, 1449178131},
				{2, 1449178141},
				{3, 1449178151},
				{4, 1449178161},
			},
			consolidation.Sum,
			2,
			[]Point{
				{3, 1449178141},
				{7, 1449178161},
			},
		},
	}
	validate(cases, t)
}

type c struct {
	numPoints     uint32
	maxDataPoints uint32
	every         uint32
}

func TestAggEvery(t *testing.T) {
	cases := []c{
		{60, 80, 1},
		{70, 80, 1},
		{79, 80, 1},
		{80, 80, 1},
		{81, 80, 2},
		{120, 80, 2},
		{150, 80, 2},
		{158, 80, 2},
		{159, 80, 2},
		{160, 80, 2},
		{161, 80, 3},
		{165, 80, 3},
		{180, 80, 3},
	}
	for i, c := range cases {
		every := aggEvery(c.numPoints, c.maxDataPoints)
		if every != c.every {
			t.Fatalf("output for testcase %d mismatch: expected: %v, got: %v", i, c.every, every)
		}
	}
}

type fixc struct {
	in       []Point
	from     uint32
	to       uint32
	interval uint32
	out      []Point
}

func nullPoints(from, to, interval uint32) []Point {
	out := make([]Point, 0)
	for i := from; i < to; i += interval {
		out = append(out, Point{math.NaN(), i})
	}
	return out
}

func TestFix(t *testing.T) {
	cases := []fixc{
		{
			// the most standard simple case
			[]Point{{1, 10}, {2, 20}, {3, 30}},
			10,
			31,
			10,
			[]Point{{1, 10}, {2, 20}, {3, 30}},
		},
		{
			// almost... need Nan in front
			[]Point{{1, 10}, {2, 20}, {3, 30}},
			1,
			31,
			10,
			[]Point{{1, 10}, {2, 20}, {3, 30}},
		},
		{
			// need Nan in front
			[]Point{{1, 10}, {2, 20}, {3, 30}},
			0,
			31,
			10,
			[]Point{{math.NaN(), 0}, {1, 10}, {2, 20}, {3, 30}},
		},
		{
			// almost..need Nan in back
			[]Point{{1, 10}, {2, 20}, {3, 30}},
			10,
			40,
			10,
			[]Point{{1, 10}, {2, 20}, {3, 30}},
		},
		{
			// need Nan in back
			[]Point{{1, 10}, {2, 20}, {3, 30}},
			10,
			41,
			10,
			[]Point{{1, 10}, {2, 20}, {3, 30}, {math.NaN(), 40}},
		},
		{
			// need Nan in middle
			[]Point{{1, 10}, {3, 30}},
			10,
			31,
			10,
			[]Point{{1, 10}, {math.NaN(), 20}, {3, 30}},
		},
		{
			// need Nan everywhere
			[]Point{{2, 20}, {4, 40}, {7, 70}},
			0,
			90,
			10,
			[]Point{{math.NaN(), 0}, {math.NaN(), 10}, {2, 20}, {math.NaN(), 30}, {4, 40}, {math.NaN(), 50}, {math.NaN(), 60}, {7, 70}, {math.NaN(), 80}},
		},
		{
			// too much data. note that there are multiple satisfactory solutions here. this is just one of them.
			[]Point{{10, 10}, {14, 14}, {20, 20}, {26, 26}, {35, 35}},
			10,
			41,
			10,
			[]Point{{10, 10}, {14, 20}, {26, 30}, {35, 40}},
		},
		{
			// no data at all. saw this one for real
			[]Point{},
			1450242982,
			1450329382,
			600,
			nullPoints(1450243200, 1450329382, 600),
		},
		{
			// don't trip over last.
			[]Point{{1, 10}, {2, 20}, {2, 19}},
			10,
			31,
			10,
			[]Point{{1, 10}, {2, 20}, {math.NaN(), 30}},
		},
	}

	for i, c := range cases {
		got := fix(c.in, c.from, c.to, c.interval)

		if len(c.out) != len(got) {
			t.Fatalf("output for testcase %d mismatch: expected: %v, got: %v", i, c.out, got)
		}
		for j, pgot := range got {
			pexp := c.out[j]
			gotNan := math.IsNaN(pgot.Val)
			expNan := math.IsNaN(pexp.Val)
			if gotNan != expNan || (!gotNan && pgot.Val != pexp.Val) || pgot.Ts != pexp.Ts {
				t.Fatalf("output for testcase %d at point %d mismatch: expected: %v, got: %v", i, j, c.out, got)
			}
		}
	}

}

type alignCase struct {
	reqs        []Req
	aggSettings []aggSetting
	outReqs     []Req
	outErr      error
}

func reqRaw(key string, from, to, maxPoints uint32, consolidator consolidation.Consolidator, rawInterval uint32) Req {
	req := NewReq(key, key, from, to, maxPoints, consolidator)
	req.rawInterval = rawInterval
	return req
}
func reqOut(key string, from, to, maxPoints uint32, consolidator consolidation.Consolidator, rawInterval uint32, archive int, archInterval, outInterval, aggNum uint32) Req {
	req := NewReq(key, key, from, to, maxPoints, consolidator)
	req.rawInterval = rawInterval
	req.archive = archive
	req.archInterval = archInterval
	req.outInterval = outInterval
	req.aggNum = aggNum
	return req
}

func TestAlignRequests(t *testing.T) {
	input := []alignCase{
		{
			// request would be satisfied by each archive like so:
			// remember we don't count 1 chunk because it can be almost-empty
			// -1 raw: 2400/10=240 points in RAM, (3600-2400)/10=120 in cassandra, 360 in total
			// 0 agg 1: 1*600/60= 10 points in RAM, (3600-600)=3000/60=50 in cassandra, 60 in total
			// 1 agg 2: 3600/120=30 points in total, none in RAM
			// only raw has enough points
			[]Req{
				reqRaw("a", 0, 3600, 800, consolidation.Avg, 10),
				reqRaw("b", 0, 3600, 800, consolidation.Avg, 10),
				reqRaw("c", 0, 3600, 800, consolidation.Avg, 10),
			},
			[]aggSetting{
				{60, 600, 2, 0},
				{120, 600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600, 800, consolidation.Avg, 10, 0, 10, 10, 1),
				reqOut("b", 0, 3600, 800, consolidation.Avg, 10, 0, 10, 10, 1),
				reqOut("c", 0, 3600, 800, consolidation.Avg, 10, 0, 10, 10, 1),
			},
			nil,
		},
		{ // now we request 0-2400, with max datapoints 100.
			// raw would provide 240pts, so runtime consolidation would be needed.
			// 60s rollups would provide 40pts which is a good candidate.
			// however 40pts is 2.5x smaller then the target 100pts and 240pts is
			// only 2.4x larger then the target 100pts, so it is selected.
			[]Req{
				reqRaw("a", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("c", 0, 2400, 100, consolidation.Avg, 10),
			},
			[]aggSetting{
				{60, 600, 2, 0},
				{120, 600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 100, consolidation.Avg, 10, 0, 10, 30, 3),
				reqOut("b", 0, 2400, 100, consolidation.Avg, 10, 0, 10, 30, 3),
				reqOut("c", 0, 2400, 100, consolidation.Avg, 10, 0, 10, 30, 3),
			},
			nil,
		},
		// same thing as above, but now we set max points to 39. So now the 240pts
		// provided by raw is 6.15x our target of 39pts. But our the 20pts provided
		// by our 120s rollups is only 1.95x smaller then our target 39pts so it is
		// selected.
		{
			[]Req{
				reqRaw("a", 0, 2400, 39, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 39, consolidation.Avg, 10),
				reqRaw("c", 0, 2400, 39, consolidation.Avg, 10),
			},
			[]aggSetting{
				{120, 600, 2, 0},
				{600, 600, 2, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 39, consolidation.Avg, 10, 1, 120, 120, 1),
				reqOut("b", 0, 2400, 39, consolidation.Avg, 10, 1, 120, 120, 1),
				reqOut("c", 0, 2400, 39, consolidation.Avg, 10, 1, 120, 120, 1),
			},
			nil,
		},
		// now something a bit different. 3 different raw intervals, but same aggregation settings.
		// raw is here best again but all series need to be at a step of 60
		// so runtime consolidation is needed, we'll get 40 points for each metric
		{
			[]Req{
				reqRaw("a", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 100, consolidation.Avg, 30),
				reqRaw("c", 0, 2400, 100, consolidation.Avg, 60),
			},
			[]aggSetting{
				{120, 600, 2, 0},
				{600, 600, 2, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 100, consolidation.Avg, 10, 0, 10, 60, 6),
				reqOut("b", 0, 2400, 100, consolidation.Avg, 30, 0, 30, 60, 2),
				reqOut("c", 0, 2400, 100, consolidation.Avg, 60, 0, 60, 60, 1),
			},
			nil,
		},

		// Similar to above with 3 different raw intervals, but these raw intervals
		// require a little more calculation to get the minimum interval they all fit into.
		// because the minimum interval that they all fit into (300) is greater then the
		// 120second rollup data, the rollups is a better choice.
		{
			[]Req{
				reqRaw("a", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 100, consolidation.Avg, 50),
				reqRaw("c", 0, 2400, 100, consolidation.Avg, 60),
			},
			[]aggSetting{
				{120, 600, 2, 0},
				{600, 600, 2, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 100, consolidation.Avg, 10, 1, 120, 120, 1),
				reqOut("b", 0, 2400, 100, consolidation.Avg, 50, 1, 120, 120, 1),
				reqOut("c", 0, 2400, 100, consolidation.Avg, 60, 1, 120, 120, 1),
			},
			nil,
		},
		// again with 3 different raw intervals that have a large common interval.
		// With this test, our common raw interval matches our first rollup. Runtime consolidation is expensive
		// so we preference the rollup data.
		{
			[]Req{
				reqRaw("a", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 100, consolidation.Avg, 50),
				reqRaw("c", 0, 2400, 100, consolidation.Avg, 60),
			},
			[]aggSetting{
				{300, 600, 2, 0},
				{600, 600, 2, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 100, consolidation.Avg, 10, 1, 300, 300, 1),
				reqOut("b", 0, 2400, 100, consolidation.Avg, 50, 1, 300, 300, 1),
				reqOut("c", 0, 2400, 100, consolidation.Avg, 60, 1, 300, 300, 1),
			},
			nil,
		},
		// again with 3 different raw intervals that have a large common interval.
		// With this test, our common raw interval is less then our first rollup so is selected.
		{
			[]Req{
				reqRaw("a", 0, 2400, 100, consolidation.Avg, 10),
				reqRaw("b", 0, 2400, 100, consolidation.Avg, 50),
				reqRaw("c", 0, 2400, 100, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 600, 2, 0},
				{1200, 1200, 2, 0},
			},
			[]Req{
				reqOut("a", 0, 2400, 100, consolidation.Avg, 10, 0, 10, 300, 30),
				reqOut("b", 0, 2400, 100, consolidation.Avg, 50, 0, 50, 300, 6),
				reqOut("c", 0, 2400, 100, consolidation.Avg, 60, 0, 60, 300, 5),
			},
			nil,
		},
		// let's do a realistic one: request 3h worth of data
		// this should come out of RAM
		{
			[]Req{
				reqRaw("a", 0, 3600*3, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*3, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*3, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*3, 1000, consolidation.Avg, 10, 0, 10, 60, 6),
				reqOut("b", 0, 3600*3, 1000, consolidation.Avg, 30, 0, 30, 60, 2),
				reqOut("c", 0, 3600*3, 1000, consolidation.Avg, 60, 0, 60, 60, 1),
			},
			nil,
		},
		// same but request 6h worth of data
		// this should come out of RAM
		{
			[]Req{
				reqRaw("a", 0, 3600*6, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*6, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*6, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*6, 1000, consolidation.Avg, 10, 0, 10, 60, 6),
				reqOut("b", 0, 3600*6, 1000, consolidation.Avg, 30, 0, 30, 60, 2),
				reqOut("c", 0, 3600*6, 1000, consolidation.Avg, 60, 0, 60, 60, 1),
			},
			nil,
		},
		// same but request 9h worth of data
		// this should come out of raw archive, mostly out of ram
		{
			[]Req{
				reqRaw("a", 0, 3600*9, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*9, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*9, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*9, 1000, consolidation.Avg, 10, 0, 10, 60, 6),
				reqOut("b", 0, 3600*9, 1000, consolidation.Avg, 30, 0, 30, 60, 2),
				reqOut("c", 0, 3600*9, 1000, consolidation.Avg, 60, 0, 60, 60, 1),
			},
			nil,
		},
		// same but request 24h worth of data
		// raw archive at 60s step would be 1440 points, which is too many
		// we can runtime consolidate raw down to 120s step, but require 18h of data from c* at 10/30/60 res
		// first agg at 600s step can return 144 points without runtime consolidation, needing 24h of data from c* at 600s, which is the better deal
		{
			[]Req{
				reqRaw("a", 0, 3600*24, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*24, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*24, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*24, 1000, consolidation.Avg, 10, 1, 600, 600, 1),
				reqOut("b", 0, 3600*24, 1000, consolidation.Avg, 30, 1, 600, 600, 1),
				reqOut("c", 0, 3600*24, 1000, consolidation.Avg, 60, 1, 600, 600, 1),
			},
			nil,
		},
		// same but now let's request 2 weeks worth of data.
		// not using raw is a no brainer.
		// first archive can return 3600*24*7 / 600 = 1008 points, which is too many, so must also do runtime consolidation and bring it back to 504
		// 2nd archive can do it in 3600*24*7 / 7200 = 84 points, but that's not enough to satisfy mindatapoints, so we should use first archive + runtime consol
		{
			[]Req{
				reqRaw("a", 0, 3600*24*7, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*24*7, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*24*7, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*24*7, 1000, consolidation.Avg, 10, 1, 600, 1200, 2),
				reqOut("b", 0, 3600*24*7, 1000, consolidation.Avg, 30, 1, 600, 1200, 2),
				reqOut("c", 0, 3600*24*7, 1000, consolidation.Avg, 60, 1, 600, 1200, 2),
			},
			nil,
		},
		// let's request 1 year of data
		// raw 3600*24*365/60 -> 525600
		// agg1 3600*24*365/600 -> 52560
		// agg2 3600*24*365/7200 -> 4380
		// agg3 3600*24*365/21600 -> 1460
		// clearly agg3 is the best, and we have to runtime consolidate wih aggNum 2
		{
			[]Req{
				reqRaw("a", 0, 3600*24*365, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*24*365, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*24*365, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{
				{600, 21600, 1, 0}, // aggregations stored in 6h chunks
				{7200, 21600, 1, 0},
				{21600, 21600, 1, 0},
			},
			[]Req{
				reqOut("a", 0, 3600*24*365, 1000, consolidation.Avg, 10, 3, 21600, 43200, 2),
				reqOut("b", 0, 3600*24*365, 1000, consolidation.Avg, 30, 3, 21600, 43200, 2),
				reqOut("c", 0, 3600*24*365, 1000, consolidation.Avg, 60, 3, 21600, 43200, 2),
			},
			nil,
		},

		// now let's request 1 year of data again, but without actually having any aggregation bands (wowa don't do this)
		// raw 3600*24*365/60 -> 525600
		// we need an aggNum of 526 to keep this under 1000 points
		{
			[]Req{
				reqRaw("a", 0, 3600*24*365, 1000, consolidation.Avg, 10),
				reqRaw("b", 0, 3600*24*365, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*24*365, 1000, consolidation.Avg, 60),
			},
			[]aggSetting{},
			[]Req{
				reqOut("a", 0, 3600*24*365, 1000, consolidation.Avg, 10, 0, 10, 31560, 526*6),
				reqOut("b", 0, 3600*24*365, 1000, consolidation.Avg, 30, 0, 30, 31560, 526*2),
				reqOut("c", 0, 3600*24*365, 1000, consolidation.Avg, 60, 0, 60, 31560, 526),
			},
			nil,
		},
		// same thing but if the metrics have the same resolution
		// raw 3600*24*365/60 -> 525600
		// we need an aggNum of 526 to keep this under 1000 points
		{
			[]Req{
				reqRaw("a", 0, 3600*24*365, 1000, consolidation.Avg, 30),
				reqRaw("b", 0, 3600*24*365, 1000, consolidation.Avg, 30),
				reqRaw("c", 0, 3600*24*365, 1000, consolidation.Avg, 30),
			},
			[]aggSetting{},
			[]Req{
				reqOut("a", 0, 3600*24*365, 1000, consolidation.Avg, 30, 0, 30, 31560, 526*2),
				reqOut("b", 0, 3600*24*365, 1000, consolidation.Avg, 30, 0, 30, 31560, 526*2),
				reqOut("c", 0, 3600*24*365, 1000, consolidation.Avg, 30, 0, 30, 31560, 526*2),
			},
			nil,
		},
	}
	for i, ac := range input {
		out, err := alignRequests(ac.reqs, ac.aggSettings)
		if err != ac.outErr {
			t.Errorf("different err value for testcase %d  expected: %v, got: %v", i, ac.outErr, err)
		}
		if len(out) != len(ac.outReqs) {
			t.Errorf("different amount of requests for testcase %d  expected: %v, got: %v", i, len(ac.outReqs), len(out))
		} else {
			for r, exp := range ac.outReqs {
				if exp != out[r] {
					t.Errorf("testcase %d, request %d:\nexpected: %v\n     got: %v", i, r, exp.DebugString(), out[r].DebugString())
				}
			}
		}
	}
}

var result []Req

func BenchmarkAlignRequests(b *testing.B) {
	var res []Req
	reqs := []Req{
		reqRaw("a", 0, 3600*24*7, 1000, consolidation.Avg, 10),
		reqRaw("b", 0, 3600*24*7, 1000, consolidation.Avg, 30),
		reqRaw("c", 0, 3600*24*7, 1000, consolidation.Avg, 60),
	}
	aggSettings := []aggSetting{
		{600, 21600, 1, 0},
		{7200, 21600, 1, 0},
		{21600, 21600, 1, 0},
	}

	for n := 0; n < b.N; n++ {
		res, _ = alignRequests(reqs, aggSettings)
	}
	result = res
}
