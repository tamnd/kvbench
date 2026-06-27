package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/kvbench/harness"
)

func cmdReport(args []string) {
	in := ""
	md := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--in":
			i++
			if i < len(args) {
				in = args[i]
			}
		case "--md":
			md = true
		}
	}
	if in == "" {
		fmt.Fprintln(os.Stderr, "report: --in <results-dir> required")
		os.Exit(2)
	}
	results := loadResults(in)
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no results found in", in)
		os.Exit(1)
	}
	if md {
		fmt.Print(RenderMarkdown(results))
	} else {
		printTables(results)
	}
}

func loadResults(dir string) []harness.Result {
	var out []harness.Result
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r harness.Result
		if json.Unmarshal(b, &r) == nil && r.Engine.Name != "" {
			out = append(out, r)
		}
	}
	return out
}

func printTables(rs []harness.Result) {
	byWl := groupByWorkload(rs)
	for _, wl := range sortedKeys(byWl) {
		fmt.Printf("\n== %s ==\n", wl)
		rows := byWl[wl]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Throughput.SustainedOps > rows[j].Throughput.SustainedOps })
		fmt.Printf("%-12s %14s %10s %10s %10s %8s\n", "engine", "ops/sec", "p50", "p99", "p99.9", "spaceAmp")
		for _, r := range rows {
			if r.Error != "" {
				fmt.Printf("%-12s  %s\n", r.Engine.Name, r.Error)
				continue
			}
			fmt.Printf("%-12s %14.0f %10s %10s %10s %8s\n",
				r.Engine.Name, r.Throughput.SustainedOps,
				ns(r.LatencyNs.P50), ns(r.LatencyNs.P99), ns(r.LatencyNs.P999),
				amp(r.Amplification.SpaceAmp))
		}
	}
}

// RenderMarkdown produces the result report markdown shipped to the spec.
func RenderMarkdown(rs []harness.Result) string {
	var b strings.Builder
	env := rs[0].Environment
	fmt.Fprintf(&b, "## Run environment\n\n")
	fmt.Fprintf(&b, "- Host: %s %s/%s, %d CPU, %s\n", env.CPUModel, env.OS, env.Arch, env.NumCPU, humanBytes(env.MemBytes))
	fmt.Fprintf(&b, "- Go: %s, GOMAXPROCS=%d\n", env.GoVersion, env.GOMAXPROCS)
	fmt.Fprintf(&b, "- kvbench: %s\n\n", rs[0].Kvbench)

	byWl := groupByWorkload(rs)
	for _, wl := range sortedKeys(byWl) {
		rows := byWl[wl]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Error != "" || rows[j].Error != "" {
				return rows[i].Error == "" && rows[j].Error != ""
			}
			return rows[i].Throughput.SustainedOps > rows[j].Throughput.SustainedOps
		})
		fmt.Fprintf(&b, "### %s\n\n", wl)
		fmt.Fprintf(&b, "| engine | family | mode | ops/sec | p50 | p99 | p99.9 | max | space-amp | GC p99 |\n")
		fmt.Fprintf(&b, "|--------|--------|------|--------:|----:|----:|------:|----:|----------:|-------:|\n")
		for _, r := range rows {
			if r.Error != "" {
				fmt.Fprintf(&b, "| %s | %s | %s | _%s_ | | | | | | |\n", r.Engine.Name, r.Engine.Family, r.Engine.Mode, mdEsc(r.Error))
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				r.Engine.Name, r.Engine.Family, r.Engine.Mode,
				comma(r.Throughput.SustainedOps),
				ns(r.LatencyNs.P50), ns(r.LatencyNs.P99), ns(r.LatencyNs.P999), ns(r.LatencyNs.Max),
				amp(r.Amplification.SpaceAmp), gcns(r.GoRuntime.GCPauseP99Ns))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func groupByWorkload(rs []harness.Result) map[string][]harness.Result {
	m := map[string][]harness.Result{}
	for _, r := range rs {
		key := r.Workload.Name
		m[key] = append(m[key], r)
	}
	return m
}

func sortedKeys(m map[string][]harness.Result) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ns(v uint64) string {
	if v == 0 {
		return "-"
	}
	return time.Duration(v).String()
}
func gcns(v uint64) string {
	if v == 0 {
		return "-"
	}
	return time.Duration(v).String()
}
func amp(a float64) string {
	if a < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2fx", a)
}
func comma(f float64) string {
	n := int64(f)
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
func humanBytes(b int64) string {
	if b < 0 {
		return "?"
	}
	const u = 1024
	if b < u {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.0f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
func mdEsc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
