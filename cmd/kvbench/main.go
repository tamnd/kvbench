// Command kvbench is the engine-neutral key/value benchmark runner.
//
// The default build compiles only the pure-Go, no-cgo adapters so
// `go install` works with zero system dependencies. Heavy adapters are behind
// build tags: -tags cgo_engines (LMDB, ...).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/kvbench/engine"
	"github.com/tamnd/kvbench/harness"
	"github.com/tamnd/kvbench/hdr"
	"github.com/tamnd/kvbench/workload"

	// Pure-Go adapters, always compiled in.
	_ "github.com/tamnd/kvbench/adapters/badger"
	_ "github.com/tamnd/kvbench/adapters/bbolt"
	_ "github.com/tamnd/kvbench/adapters/buntdb"
	_ "github.com/tamnd/kvbench/adapters/f2"
	_ "github.com/tamnd/kvbench/adapters/goleveldb"
	_ "github.com/tamnd/kvbench/adapters/inmem"
	_ "github.com/tamnd/kvbench/adapters/kv"
	_ "github.com/tamnd/kvbench/adapters/memory"
	_ "github.com/tamnd/kvbench/adapters/pebble"
	_ "github.com/tamnd/kvbench/adapters/pogreb"
	_ "github.com/tamnd/kvbench/adapters/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		cmdList()
	case "run":
		cmdRun(os.Args[2:])
	case "report":
		cmdReport(os.Args[2:])
	case "compare":
		cmdCompare(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `kvbench - engine-neutral key/value benchmark

usage:
  kvbench list
  kvbench run     [flags]
  kvbench report  --in <results-dir> [--md]
  kvbench compare --kv <kv-results-dir> --baseline <baseline-results-dir> [--exclude eng,eng] [--md]

run flags:
  --engines a,b,c   engines to run (default: all built-in real stores; the
                    reference rails are skipped unless named explicitly)
  --workloads a,b   workloads (default: all)
  --regimes a,b     cache-resident,out-of-cache (default: cache-resident)
  --durability a,b  DEFAULT,FULL (default: DEFAULT). DEFAULT is each engine as it
                    ships, its own background durability; FULL forces per-commit
                    fsync on every engine. Both are durable, they differ in when
                    the fsync lands, not whether one happens.
  --values a,b      value sizes in bytes (default: 1024)
  --conc a,b        concurrency levels (default: 8)
  --cardinality N   keys to load (default: 100000)
  --ops N           measured ops per cell (default: 200000)
  --reps N          repetitions per cell (default: 3)
  --seed N          base seed (default: 1)
  --out DIR         results dir (default: results/run-<seed>)
`)
}

func cmdList() {
	fmt.Printf("%-12s %-10s %-9s %-18s %s\n", "NAME", "FAMILY", "MODE", "VERSION", "CAPS")
	for _, n := range engine.Names() {
		e, _ := engine.New(n)
		m := e.Meta()
		var caps []string
		if m.Caps.Ordered {
			caps = append(caps, "ordered")
		}
		if m.Caps.Transactions {
			caps = append(caps, "txn")
		}
		if m.Caps.SingleFile {
			caps = append(caps, "single-file")
		}
		if m.Caps.PureNoCgo {
			caps = append(caps, "no-cgo")
		}
		if m.Caps.Durable {
			caps = append(caps, "durable")
		}
		if m.Reference {
			caps = append(caps, "reference")
		}
		fmt.Printf("%-12s %-10s %-9s %-18s %s\n", m.Name, m.Family, m.Mode, m.Version, strings.Join(caps, ","))
	}
}

type runFlags struct {
	engines     []string
	workloads   []string
	regimes     []string
	durability  []string
	values      []int
	conc        []int
	cardinality uint64
	ops         uint64
	reps        int
	seed        uint64
	out         string
}

func cmdRun(args []string) {
	f := runFlags{
		regimes:     []string{"cache-resident"},
		durability:  []string{"DEFAULT"},
		values:      []int{1024},
		conc:        []int{8},
		cardinality: 100000,
		ops:         200000,
		reps:        3,
		seed:        1,
	}
	parse(args, &f)
	if len(f.engines) == 0 {
		f.engines = engine.DefaultNames()
	}
	if len(f.workloads) == 0 {
		f.workloads = sortedWorkloads()
	}
	if f.out == "" {
		f.out = fmt.Sprintf("results/run-%d", f.seed)
	}
	if err := os.MkdirAll(f.out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create out dir %q: %v\n", f.out, err)
		os.Exit(1)
	}

	runID := fmt.Sprintf("run-%d", f.seed)
	dataRoot, _ := os.MkdirTemp("", "kvbench-data-")
	defer func() { _ = os.RemoveAll(dataRoot) }()

	ctx := context.Background()
	var all []harness.Result
	total := len(f.engines) * len(f.workloads) * len(f.regimes) * len(f.durability) * len(f.values) * len(f.conc)
	done := 0
	start := time.Now()

	for _, en := range f.engines {
		if !engine.Has(en) {
			fmt.Fprintf(os.Stderr, "skip %s: not built into this binary\n", en)
			continue
		}
		for _, wn := range f.workloads {
			spec, ok := workload.Catalog[wn]
			if !ok {
				continue
			}
			for _, regime := range f.regimes {
				for _, dur := range f.durability {
					// durability sweep only matters for write-bearing workloads
					for _, val := range f.values {
						for _, c := range f.conc {
							done++
							cache := cacheFor(regime, f.cardinality, val)
							cell := harness.CellConfig{
								EngineName:  en,
								Workload:    spec,
								Regime:      regime,
								Profile:     "tuned",
								Durability:  dur,
								Concurrency: c,
								ValueBytes:  val,
								Cardinality: f.cardinality,
								Operations:  f.ops,
								Seed:        f.seed,
								RunID:       runID,
								DataRoot:    dataRoot,
								CacheBytes:  cache,
							}
							res := runReps(ctx, cell, f.reps)
							all = append(all, res)
							writeResult(f.out, res)
							status := "ok"
							if res.Error != "" {
								status = res.Error
							}
							fmt.Printf("[%d/%d] %-10s %-12s %-14s val=%-6d c=%-2d dur=%-6s  %12.0f ops/s  p99=%-8s  %s\n",
								done, total, en, wn, regime, val, c, dur,
								res.Throughput.SustainedOps, dur2(res.LatencyNs.P99), status)
						}
					}
				}
			}
		}
	}
	fmt.Printf("\ndone: %d cells in %s -> %s\n", len(all), time.Since(start).Round(time.Second), f.out)
}

// runReps runs a cell N times and aggregates: throughput median/min/max,
// latency from the median-throughput rep.
func runReps(ctx context.Context, cell harness.CellConfig, reps int) harness.Result {
	if reps < 1 {
		reps = 1
	}
	var results []harness.Result
	var tputs []float64
	for i := 0; i < reps; i++ {
		c := cell
		c.Seed = cell.Seed + uint64(i)*1_000_003
		r := harness.RunCell(ctx, c)
		results = append(results, r)
		if r.Error == "" {
			tputs = append(tputs, r.Throughput.SustainedOps)
		}
	}
	if len(tputs) == 0 {
		return results[0] // carries the error
	}
	med := hdr.MedianOf(tputs)
	// pick the rep whose throughput is closest to the median for the latency view
	best := 0
	bestd := 1e18
	for i, r := range results {
		if r.Error != "" {
			continue
		}
		d := abs(r.Throughput.SustainedOps - med)
		if d < bestd {
			bestd, best = d, i
		}
	}
	out := results[best]
	out.Repetitions = reps
	out.Throughput.SustainedOps = med
	out.Throughput.Min = minOf(tputs)
	out.Throughput.Max = maxOf(tputs)
	return out
}

func cacheFor(regime string, card uint64, val int) int64 {
	working := int64(card) * int64(val+16)
	switch regime {
	case "out-of-cache":
		// constrain cache to ~1/8 of the working set so reads miss to disk.
		c := working / 8
		if c < 1<<20 {
			c = 1 << 20
		}
		return c
	default: // cache-resident: cache >= working set
		c := working + working/4
		if c < 8<<20 {
			c = 8 << 20
		}
		return c
	}
}

func writeResult(dir string, r harness.Result) {
	name := fmt.Sprintf("%s__%s__%s__v%d__c%d__%s.json",
		r.Engine.Name, r.Workload.Name, r.Workload.Regime, r.Workload.ValueBytes, r.Workload.Concurrency, r.Workload.Durability)
	name = strings.ReplaceAll(name, "/", "_")
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(dir+"/"+name, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write result %q: %v\n", name, err)
	}
}

// ---- helpers ----

func sortedWorkloads() []string {
	out := make([]string, 0, len(workload.Catalog))
	for k := range workload.Catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parse(args []string, f *runFlags) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch a {
		case "--engines":
			f.engines = splitCSV(next())
		case "--workloads":
			f.workloads = splitCSV(next())
		case "--regimes":
			f.regimes = splitCSV(next())
		case "--durability":
			f.durability = normalizeDurability(splitCSV(next()))
		case "--values":
			f.values = splitCSVInt(next())
		case "--conc":
			f.conc = splitCSVInt(next())
		case "--cardinality":
			f.cardinality = uint64(atoi(next()))
		case "--ops":
			f.ops = uint64(atoi(next()))
		case "--reps":
			f.reps = atoi(next())
		case "--seed":
			f.seed = uint64(atoi(next()))
		case "--out":
			f.out = next()
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", a)
		}
	}
}

// normalizeDurability keeps the durability axis to the two regimes the benchmark exposes,
// DEFAULT and FULL. Both are durable: DEFAULT runs each engine at its own shipped durability,
// FULL forces a per-commit fsync on every engine. The older OFF and NORMAL names are gone
// because they read as "durability off" when they were not, so a caller who passes one gets a
// clear pointer to the two names that remain rather than a silently mismapped run.
func normalizeDurability(vals []string) []string {
	out := vals[:0]
	for _, v := range vals {
		u := strings.ToUpper(strings.TrimSpace(v))
		switch u {
		case "DEFAULT", "FULL":
			out = append(out, u)
		case "OFF", "NORMAL":
			fmt.Fprintf(os.Stderr, "durability %q is no longer a regime; use DEFAULT (each engine as it ships) or FULL (per-commit fsync)\n", v)
			os.Exit(2)
		default:
			fmt.Fprintf(os.Stderr, "unknown durability %q; use DEFAULT or FULL\n", v)
			os.Exit(2)
		}
	}
	return out
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitCSVInt(s string) []int {
	var out []int
	for _, p := range splitCSV(s) {
		out = append(out, atoi(p))
	}
	return out
}

func atoi(s string) int {
	n := 0
	neg := false
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x < m {
			m = x
		}
	}
	return m
}
func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func dur2(ns uint64) string {
	d := time.Duration(ns)
	return d.String()
}
