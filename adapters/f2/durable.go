// This file adds the durable sibling of the memory-only kv-f2 adapter. Where kv-f2
// measures the compact-index in-memory ceiling (Path empty, nothing fsynced),
// kv-f2-durable opens the single-file layout from spec 2070: one file at
// cfg.Dir/data.f2 that is both the larger-than-memory backing and, under a Normal
// or Full dial, the crash-recoverable store with no lost acknowledged write. There
// is no separate scratch design; the file that holds an evicted page is the one a
// crash recovers from. It declares Durable:true and SingleFile:true and carries no
// memory-only asterisk.
//
// The mapping doc 08 section 5 specifies:
//   - the durability dial: OFF -> None, NORMAL -> Normal, FULL -> Full, so a
//     dial-matched cell pairs this engine's barrier against the competitor's equal.
//   - the regime: cfg.CacheBytes selects ResidentPagesPerShard. Zero keeps the
//     full-resident in-cache regime; a budget below the working set is the
//     eviction-possible out-of-cache regime that exercises the pread read path.
//   - one Path, so the bench measures the shipped single-file store.
package f2

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/tamnd/kv/f2"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-f2-durable", func() engine.Engine { return &durable{} })
}

// durableDefaults match the memory-only ceiling's shape (256 shards, 1 MiB pages),
// so the durable cell measures what the single-file layout costs against the same
// index and page geometry the ceiling was tuned on, not a different store.
const (
	durableDefaultShards   = 256
	durableDefaultPageSize = 1 << 20
)

type durable struct {
	s *f2.Store
}

func (e *durable) Meta() engine.Meta {
	return engine.Meta{
		Name: "kv-f2-durable", Family: engine.FamilyHashLog, Mode: engine.ModeInProc, Reference: true,
		Version: "main",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: false, Durable: true, Transactions: false,
			OnlineBackup: false, SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "unordered", Note: "kv-f2-durable has no ordered scan; range and scan workloads do not apply and are skipped for it."},
			{Code: "checkpoint-on-flush", Note: "kv-f2-durable takes a checkpoint when the harness calls Flush; between flushes recovery replays the log delta since the last checkpoint, which is the engine's normal recovery path, not an unflushed-data risk under the NORMAL and FULL dials."},
		},
	}
}

// syncDial maps the kvbench durability contract onto the f2 dial. The default
// (DEFAULT/unset) is NORMAL, the group-commit floor, matching how every other
// adapter treats its unspecified case.
func syncDial(s string) f2.Durability {
	switch s {
	case "OFF":
		return f2.DurabilityNone
	case "FULL":
		return f2.DurabilityFull
	default:
		return f2.DurabilityNormal
	}
}

// atoiOr reads an integer tuning key from the profile's Extra map, falling back to
// def when the key is absent or unparseable.
func atoiOr(m map[string]string, key string, def int) int {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (e *durable) Open(_ context.Context, cfg engine.Config) error {
	shards := atoiOr(cfg.Extra, "shards", durableDefaultShards)
	pageSize := atoiOr(cfg.Extra, "page_bytes", durableDefaultPageSize)

	// The regime is set by the resident budget. Zero CacheBytes keeps the
	// full-resident in-cache regime (ResidentPagesPerShard 0, nothing evicts). A
	// positive budget divides into per-shard resident pages, the eviction-possible
	// out-of-cache regime, floored at one page so a tiny budget still opens.
	resident := 0
	if cfg.CacheBytes > 0 {
		resident = int(cfg.CacheBytes) / (shards * pageSize)
		if resident < 1 {
			resident = 1
		}
	}

	t := f2.Tunables{
		Shards:                shards,
		PageSize:              pageSize,
		ResidentPagesPerShard: resident,
		Path:                  filepath.Join(cfg.Dir, "data.f2"),
		Durability:            syncDial(cfg.Synchronous),
	}
	s, err := f2.New(t)
	if err != nil {
		return err
	}
	e.s = s
	return nil
}

func (e *durable) Get(_ context.Context, key []byte) ([]byte, bool, error) { return e.s.Get(key) }
func (e *durable) Put(_ context.Context, key, value []byte) error          { return e.s.Set(key, value) }
func (e *durable) Delete(_ context.Context, key []byte) error              { return e.s.Delete(key) }
func (e *durable) NewBatch() engine.Batch                                  { return &durableBatch{e: e} }

// Scan is unsupported: the engine is unordered. The driver skips scan workloads for
// it because Caps.Ordered is false.
func (e *durable) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return emptyIter{}, nil
}

// Flush takes a checkpoint, the durable engine's bound on recovery replay, the same
// way the kv adapter maps Flush onto a checkpoint.
func (e *durable) Flush(_ context.Context) error                 { return e.s.Checkpoint() }
func (e *durable) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *durable) Close(_ context.Context) error {
	if e.s != nil {
		return e.s.Close()
	}
	return nil
}

// durableBatch applies buffered writes one at a time; the engine has no atomic
// batch, so this is a convenience grouping, not a transaction (the same shape the
// memory-only adapter's batch takes).
type durableBatch struct {
	e   *durable
	ops []op
}

func (b *durableBatch) Put(k, v []byte) {
	b.ops = append(b.ops, op{k: append([]byte(nil), k...), v: append([]byte(nil), v...)})
}
func (b *durableBatch) Delete(k []byte) {
	b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)})
}
func (b *durableBatch) Len() int { return len(b.ops) }
func (b *durableBatch) Commit(_ context.Context) error {
	for _, o := range b.ops {
		if o.del {
			if err := b.e.s.Delete(o.k); err != nil {
				return err
			}
		} else if err := b.e.s.Set(o.k, o.v); err != nil {
			return err
		}
	}
	b.ops = nil
	return nil
}
