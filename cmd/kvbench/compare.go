package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tamnd/kvbench/harness"
)

// cmdCompare pairs kv against the best competitor per workload and reports the
// throughput ratio and whether it clears the campaign's 5x bar. The kv rows are
// read from --kv (the integrated re-run, which only re-runs kv-btree and kv-lsm),
// and the competitor rows from --baseline (the frozen all-engine reference), so
// the two halves of the campaign measurement stay separate files: freeze the
// competitors once, re-run only kv. This is the gap table the 5x spec cites.
func cmdCompare(args []string) {
	kvDir, baseDir := "", ""
	exclude := map[string]bool{}
	md := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--kv":
			i++
			if i < len(args) {
				kvDir = args[i]
			}
		case "--baseline":
			i++
			if i < len(args) {
				baseDir = args[i]
			}
		case "--exclude":
			i++
			if i < len(args) {
				for _, e := range strings.Split(args[i], ",") {
					if e = strings.TrimSpace(e); e != "" {
						exclude[e] = true
					}
				}
			}
		case "--md":
			md = true
		}
	}
	if kvDir == "" || baseDir == "" {
		fmt.Fprintln(os.Stderr, "compare: --kv <kv-results-dir> and --baseline <baseline-results-dir> required")
		os.Exit(2)
	}
	kvRows := loadResults(kvDir)
	baseRows := loadResults(baseDir)
	if len(kvRows) == 0 {
		fmt.Fprintln(os.Stderr, "compare: no kv results in", kvDir)
		os.Exit(1)
	}
	if len(baseRows) == 0 {
		fmt.Fprintln(os.Stderr, "compare: no baseline results in", baseDir)
		os.Exit(1)
	}
	rows := buildComparison(kvRows, baseRows, exclude)
	if md {
		fmt.Print(renderCompareMarkdown(rows, exclude))
	} else {
		printCompare(rows)
	}
}

// isKVEngine reports whether name is one of kv's own engines, which are read from
// the kv re-run dir, not treated as competitors.
func isKVEngine(name string) bool { return name == "kv-btree" || name == "kv-lsm" }

// cmpRow is one workload-config comparison: the two kv engines against the single
// fastest competitor at the same cell.
type cmpRow struct {
	key        string // human-readable workload-config label
	sortKey    string
	btree      float64 // kv-btree ops/sec, -1 if absent
	lsm        float64 // kv-lsm ops/sec, -1 if absent
	bestComp   float64 // fastest competitor ops/sec, -1 if none
	bestCompNm string
	kvBest     float64 // max(btree, lsm)
	kvBestNm   string
	ratio      float64 // kvBest / bestComp, -1 if not computable
}

// cellKey identifies one workload-config cell so the same workload at a different
// value size or durability does not collide. The campaign run is a single config,
// but keying on the full tuple keeps the tool correct if a dir mixes configs.
func cellKey(r harness.Result) string {
	return fmt.Sprintf("%s|%s|%s|v%d|c%d", r.Workload.Name, r.Workload.Regime,
		r.Workload.Durability, r.Workload.ValueBytes, r.Workload.Concurrency)
}

func cellLabel(r harness.Result) string {
	return fmt.Sprintf("%s (%s, v%d, c%d, %s)", r.Workload.Name, r.Workload.Regime,
		r.Workload.ValueBytes, r.Workload.Concurrency, r.Workload.Durability)
}

func buildComparison(kvRows, baseRows []harness.Result, exclude map[string]bool) []cmpRow {
	// kv engines come from the re-run dir; index them by cell then engine.
	kvByCell := map[string]map[string]harness.Result{}
	label := map[string]string{}
	for _, r := range kvRows {
		if !isKVEngine(r.Engine.Name) {
			continue
		}
		k := cellKey(r)
		if kvByCell[k] == nil {
			kvByCell[k] = map[string]harness.Result{}
			label[k] = cellLabel(r)
		}
		kvByCell[k][r.Engine.Name] = r
	}
	// best competitor per cell from the baseline dir.
	bestComp := map[string]harness.Result{}
	for _, r := range baseRows {
		if isKVEngine(r.Engine.Name) || exclude[r.Engine.Name] || r.Error != "" {
			continue
		}
		k := cellKey(r)
		cur, ok := bestComp[k]
		if !ok || r.Throughput.SustainedOps > cur.Throughput.SustainedOps {
			bestComp[k] = r
		}
	}

	var out []cmpRow
	for k, engines := range kvByCell {
		row := cmpRow{key: label[k], sortKey: k, btree: -1, lsm: -1, bestComp: -1, ratio: -1}
		if r, ok := engines["kv-btree"]; ok && r.Error == "" {
			row.btree = r.Throughput.SustainedOps
		}
		if r, ok := engines["kv-lsm"]; ok && r.Error == "" {
			row.lsm = r.Throughput.SustainedOps
		}
		row.kvBest = row.btree
		row.kvBestNm = "kv-btree"
		if row.lsm > row.kvBest {
			row.kvBest, row.kvBestNm = row.lsm, "kv-lsm"
		}
		if c, ok := bestComp[k]; ok {
			row.bestComp = c.Throughput.SustainedOps
			row.bestCompNm = c.Engine.Name
			if row.bestComp > 0 && row.kvBest > 0 {
				row.ratio = row.kvBest / row.bestComp
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sortKey < out[j].sortKey })
	return out
}

func verdict(ratio float64) string {
	switch {
	case ratio < 0:
		return "n/a"
	case ratio >= 5:
		return fmt.Sprintf("%.2fx 5x-CLEARED", ratio)
	case ratio >= 1:
		return fmt.Sprintf("%.2fx lead", ratio)
	default:
		return fmt.Sprintf("%.2fx behind", ratio)
	}
}

func opsCol(v float64) string {
	if v < 0 {
		return "-"
	}
	return comma(v)
}

func printCompare(rows []cmpRow) {
	fmt.Printf("%-44s %14s %14s %14s %-16s %s\n",
		"workload", "kv-btree", "kv-lsm", "best-comp", "competitor", "verdict (kv-best/comp)")
	for _, r := range rows {
		fmt.Printf("%-44s %14s %14s %14s %-16s %s\n",
			r.key, opsCol(r.btree), opsCol(r.lsm), opsCol(r.bestComp), r.bestCompNm, verdict(r.ratio))
	}
}

func renderCompareMarkdown(rows []cmpRow, exclude map[string]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## kv vs best competitor (5x gap table)\n\n")
	if len(exclude) > 0 {
		ex := make([]string, 0, len(exclude))
		for e := range exclude {
			ex = append(ex, e)
		}
		sort.Strings(ex)
		fmt.Fprintf(&b, "Competitors excluded from the best-of: %s.\n\n", strings.Join(ex, ", "))
	}
	fmt.Fprintf(&b, "kv rows are the integrated re-run; competitor is the fastest non-kv engine at the same cell from the frozen baseline.\n\n")
	fmt.Fprintf(&b, "| workload | kv-btree ops/s | kv-lsm ops/s | best competitor | comp ops/s | kv-best/comp | 5x |\n")
	fmt.Fprintf(&b, "|----------|---------------:|-------------:|-----------------|-----------:|-------------:|----|\n")
	for _, r := range rows {
		fiveX := ""
		switch {
		case r.ratio < 0:
			fiveX = ""
		case r.ratio >= 5:
			fiveX = "yes"
		default:
			fiveX = "no"
		}
		ratioStr := "-"
		if r.ratio >= 0 {
			ratioStr = fmt.Sprintf("%.2fx", r.ratio)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			r.key, opsCol(r.btree), opsCol(r.lsm), r.bestCompNm, opsCol(r.bestComp), ratioStr, fiveX)
	}
	b.WriteString("\n")
	return b.String()
}
