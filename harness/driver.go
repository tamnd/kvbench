package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamnd/kvbench/engine"
	"github.com/tamnd/kvbench/env"
	"github.com/tamnd/kvbench/hdr"
	"github.com/tamnd/kvbench/workload"
)

const Version = "0.1.0"

// CellConfig is one unit of work: an engine under a workload in a regime.
type CellConfig struct {
	EngineName  string
	Workload    workload.Spec
	Regime      string // cache-resident | out-of-cache
	Profile     string // default | tuned
	Durability  string // DEFAULT | FULL
	Concurrency int
	ValueBytes  int
	Cardinality uint64 // keys to load
	Operations  uint64 // measured ops (total across clients)
	Seed        uint64
	RunID       string
	DataRoot    string
	CacheBytes  int64
	// ServerCPUList pins a launched network server to these cores (taskset -c
	// list, Linux only). Set by --cpu-split; empty for embedded engines.
	ServerCPUList string
}

// RunCell loads the engine, runs the measured workload, and returns a Result.
// It never panics out: an engine failure is captured in Result.Error.
func RunCell(ctx context.Context, c CellConfig) Result {
	var r Result
	r.Schema = "kvbench/result/v1"
	r.Kvbench = Version
	r.RunID = c.RunID
	r.Seed = c.Seed
	r.Repetitions = 1
	r.Workload.Name = c.Workload.Name
	r.Workload.Regime = c.Regime
	r.Workload.Durability = c.Durability
	r.Workload.Concurrency = c.Concurrency
	r.Workload.ValueBytes = c.ValueBytes
	r.Workload.Cardinality = c.Cardinality
	r.Workload.Operations = c.Operations
	r.Environment = env.Capture()
	r.SteadyState = true
	r.Amplification.Tier = "unavailable"
	r.Amplification.SpaceAmp = -1
	r.Amplification.WriteAmp = -1
	r.Amplification.OnDiskBytes = -1

	defer func() {
		if p := recover(); p != nil {
			r.Error = fmt.Sprintf("panic: %v", p)
		}
	}()

	eng, err := engine.New(c.EngineName)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	meta := eng.Meta()
	r.Engine.Name = meta.Name
	r.Engine.Family = string(meta.Family)
	r.Engine.Mode = string(meta.Mode)
	r.Engine.Class = string(engine.ClassOf(meta))
	r.Engine.Version = meta.Version
	r.Engine.Profile = c.Profile
	r.Engine.Caps = meta.Caps
	r.Asterisks = meta.Asterisks

	// Skip workloads the engine cannot serve, honestly marked.
	if c.Workload.NeedsScan && !meta.Caps.Ordered {
		r.Error = "unsupported: workload needs ordered scan, engine is unordered"
		return r
	}

	// FULL forces a per-commit fsync. That is a real production comparison for the
	// embedded class, where people do run bbolt or sqlite synchronous=FULL, so the
	// durable-writes scenario measures it there. Over a network hop nobody runs it:
	// the round-trip already dominates and a per-commit fsync on top of it is a mode
	// no one deploys, which is why redis itself defaults to everysec and calls
	// appendfsync always prohibitively slow. So the RESP and distributed classes are
	// a DEFAULT (everysec) comparison only, and a FULL cell for a networked engine is
	// skipped rather than run under a number that would be everysec wearing a FULL label.
	if c.Durability == "FULL" {
		switch engine.ClassOf(meta) {
		case engine.ClassRedisMemory, engine.ClassRedisPersistent, engine.ClassDistributed:
			r.Error = "unsupported: FULL per-commit fsync is not measured over the network; the RESP class runs everysec (DEFAULT)"
			return r
		}
	}

	dir := filepath.Join(c.DataRoot, c.RunID, c.EngineName+"_"+c.Workload.Name+"_"+c.Regime)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.Error = "mkdir: " + err.Error()
		return r
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := engine.Config{
		Dir:         dir,
		Profile:     c.Profile,
		Synchronous: c.Durability,
		CacheBytes:  c.CacheBytes,
		ValueBytes:  c.ValueBytes,
		Cardinality: c.Cardinality,
	}
	if err := eng.Open(ctx, cfg); err != nil {
		r.Error = "open: " + err.Error()
		return r
	}
	defer func() { _ = eng.Close(ctx) }()

	// ---- LOAD PHASE (timed separately, never folded into steady state) ----
	loadStart := time.Now()
	if err := loadDataset(ctx, eng, c); err != nil {
		r.Error = "load: " + err.Error()
		return r
	}
	_ = eng.Flush(ctx)
	r.Load.Seconds = time.Since(loadStart).Seconds()
	r.Load.Ops = c.Cardinality
	if r.Load.Seconds > 0 {
		r.Load.OpsPerSec = float64(c.Cardinality) / r.Load.Seconds
	}

	// logical bytes ingested (approx): cardinality * (keyBytes + valueBytes)
	logical := int64(c.Cardinality) * int64(16+c.ValueBytes)
	r.Amplification.LogicalBytes = logical

	// ---- MEASURE PHASE ----
	// Snapshot GC metrics before.
	gcBefore := readGC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	runtime.GC()

	measStart := time.Now()
	hist, reads, writes := runMeasured(ctx, eng, c)
	elapsed := time.Since(measStart).Seconds()

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	gcAfter := readGC()

	totalOps := reads + writes
	if elapsed > 0 {
		r.Throughput.SustainedOps = float64(totalOps) / elapsed
		r.Throughput.Min = r.Throughput.SustainedOps
		r.Throughput.Max = r.Throughput.SustainedOps
		r.Throughput.ReadOps = float64(reads) / elapsed
		r.Throughput.WriteOps = float64(writes) / elapsed
	}
	r.LatencyNs = hist.Snapshot()

	// GC bundle (only meaningful for in-proc Go engines).
	if meta.Mode == engine.ModeInProc {
		r.GoRuntime.GCPauseP99Ns = gcAfter.pauseP99
		r.GoRuntime.GCPauseMaxNs = gcAfter.pauseMax
		r.GoRuntime.NumGC = memAfter.NumGC - memBefore.NumGC
		r.GoRuntime.GCCPUFrac = memAfter.GCCPUFraction
		allocBytes := int64(memAfter.TotalAlloc - memBefore.TotalAlloc)
		if totalOps > 0 {
			r.GoRuntime.AllocPerOp = float64(allocBytes) / float64(totalOps)
		}
		_ = gcBefore
	}

	// ---- AMPLIFICATION (space via dir size; write-amp via engine stats) ----
	_ = eng.Flush(ctx)
	if onDisk, err := dirSize(dir); err == nil && logical > 0 {
		r.Amplification.OnDiskBytes = onDisk
		r.Amplification.SpaceAmp = float64(onDisk) / float64(logical)
		r.Amplification.Tier = "filesystem"
	}
	if st, err := eng.Stats(ctx); err == nil && st.BytesWritten > 0 && logical > 0 {
		r.Amplification.WriteAmp = float64(st.BytesWritten) / float64(logical)
		if r.Amplification.Tier == "filesystem" {
			r.Amplification.Tier = "engine-reported+filesystem"
		} else {
			r.Amplification.Tier = "engine-reported"
		}
	}

	// DEFAULT lets each engine run at its own shipped durability, so a non-durable engine
	// needs no caveat there. FULL is an explicit per-commit fsync request that this engine
	// cannot honor, which the reader must know.
	if !meta.Caps.Durable && c.Durability == "FULL" {
		r.Asterisks = append(r.Asterisks, engine.Asterisk{Code: "no-fsync", Note: "engine cannot fsync; durability requested but not delivered"})
	}
	return r
}

// loadDataset populates the keyspace with cardinality keys, batched.
func loadDataset(ctx context.Context, eng engine.Engine, c CellConfig) error {
	const batchSize = 1000
	gen := workload.NewGenerator(workload.Spec{Name: "load", InsertPct: 100, WriteOnly: true, Sequential: true}, c.Seed, 0, c.Cardinality, c.ValueBytes)
	b := eng.NewBatch()
	for i := uint64(0); i < c.Cardinality; i++ {
		op := gen.Next()
		b.Put(op.Key, op.Value)
		if b.Len() >= batchSize {
			if err := b.Commit(ctx); err != nil {
				return err
			}
			b = eng.NewBatch()
		}
	}
	if b.Len() > 0 {
		return b.Commit(ctx)
	}
	return nil
}

// runMeasured drives the workload across c.Concurrency client goroutines and
// returns the merged latency histogram and op counts.
func runMeasured(ctx context.Context, eng engine.Engine, c CellConfig) (*hdr.Histogram, uint64, uint64) {
	perClient := c.Operations / uint64(c.Concurrency)
	if perClient == 0 {
		perClient = 1
	}
	var reads, writes atomic.Uint64
	hists := make([]*hdr.Histogram, c.Concurrency)
	var wg sync.WaitGroup
	for cl := 0; cl < c.Concurrency; cl++ {
		wg.Add(1)
		h := hdr.New()
		hists[cl] = h
		go func(clientID int, h *hdr.Histogram) {
			defer wg.Done()
			gen := workload.NewGenerator(c.Workload, c.Seed+1, clientID, c.Cardinality, c.ValueBytes)
			scratch := make([]byte, 0, c.ValueBytes)
			for i := uint64(0); i < perClient; i++ {
				op := gen.Next()
				t0 := time.Now()
				switch op.Kind {
				case workload.OpRead:
					_, _, _ = eng.Get(ctx, op.Key)
					reads.Add(1)
				case workload.OpUpdate, workload.OpInsert:
					_ = eng.Put(ctx, op.Key, op.Value)
					writes.Add(1)
				case workload.OpDelete:
					_ = eng.Delete(ctx, op.Key)
					writes.Add(1)
				case workload.OpRMW:
					v, _, _ := eng.Get(ctx, op.Key)
					scratch = append(scratch[:0], op.Value...)
					if len(v) > 0 && len(scratch) > 0 {
						scratch[0] ^= v[0]
					}
					_ = eng.Put(ctx, op.Key, scratch)
					reads.Add(1)
					writes.Add(1)
				case workload.OpScan:
					it, err := eng.Scan(ctx, op.Key)
					if err == nil {
						n := 0
						for it.Next() && n < op.ScanLen {
							_ = it.Key()
							_ = it.Value()
							n++
						}
						_ = it.Close()
					}
					reads.Add(1)
				}
				h.Record(uint64(time.Since(t0).Nanoseconds()))
			}
		}(cl, h)
	}
	wg.Wait()
	merged := hdr.New()
	for _, h := range hists {
		merged.Merge(h)
	}
	return merged, reads.Load(), writes.Load()
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

type gcSnap struct {
	pauseP99 uint64
	pauseMax uint64
}

func readGC() gcSnap {
	samples := []metrics.Sample{{Name: "/gc/pauses:seconds"}}
	metrics.Read(samples)
	var s gcSnap
	if samples[0].Value.Kind() == metrics.KindFloat64Histogram {
		h := samples[0].Value.Float64Histogram()
		var total uint64
		for _, c := range h.Counts {
			total += c
		}
		if total > 0 {
			target := uint64(float64(total) * 0.99)
			var cum uint64
			for i, c := range h.Counts {
				cum += c
				if cum >= target && i < len(h.Buckets) {
					s.pauseP99 = uint64(h.Buckets[i+1] * 1e9)
					break
				}
			}
			for i := len(h.Counts) - 1; i >= 0; i-- {
				if h.Counts[i] > 0 && i+1 < len(h.Buckets) {
					s.pauseMax = uint64(h.Buckets[i+1] * 1e9)
					break
				}
			}
		}
	}
	return s
}
