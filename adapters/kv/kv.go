// Package kv adapts github.com/tamnd/kv, the single-file embedded Go store this whole
// benchmark exists to keep honest. kv's storage core is a hot/cold hash-log: a mostly
// lock-free sharded hash index over a hybrid log, with the working set held in an
// in-memory hot tier and the cold tail spilled to one file, so a get is a hash and a
// read rather than a tree descent and the database can run far larger than memory. The
// core is exposed directly as the hlog engine (github.com/tamnd/kv/hlog), and that is
// what this cell measures: a point write in, a point read out, through the same small
// Set/Get/Delete surface every other embedded store here is driven through.
//
// kv is pure Go with no cgo and keeps its data in one file plus a small commit-watermark
// sidecar, so it sits in the single-file embedded class alongside bbolt and lmdb. It is
// unordered: the index is a hash table with no key order, so there is no scan, and range
// and scan workloads (readseq, ycsb-e) do not apply and are skipped through Caps.Ordered
// being false.
//
// kv is durable in both of its modes, and they differ only in when the fsync lands, so
// this is a granularity choice, not durable versus not. DEFAULT is background group
// commit: a write acks from the in-memory hot tier and the flusher fsyncs it a moment
// later, so a hard crash can lose only the sub-second unflushed tail, at most two
// segments, the same contract as Redis appendfsync everysec. FULL waits for the
// group-commit fsync before a write returns, so an acked write survives a crash with
// zero loss, the bbolt and per-commit sqlite contract, and concurrent writers coalesce
// onto one shared fsync rather than paying one each. Neither mode is durability off.
//
// There is no home-field advantage here. kv goes through the exact same Engine SPI as
// every other store and gets no special path in the driver.
package kv

import (
	"context"

	"github.com/tamnd/kv/hlog"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv", func() engine.Engine { return &eng{} })
}

type eng struct {
	db   *hlog.TieredDB
	sync bool // whether the harness Flush step forces a durability barrier
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "kv", Family: engine.FamilyHashLog, Mode: engine.ModeInProc,
		Class:   engine.ClassEmbedded,
		Version: "hlog",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: false, Durable: true,
			SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "group-commit", Note: "durable in both modes, the difference is only when the fsync lands. DEFAULT is background group commit: a write acks from the in-memory hot tier and the flusher fsyncs it a moment later, so a crash loses only the sub-second unflushed tail, at most two segments, the Redis appendfsync-everysec contract. FULL waits for the group-commit fsync before the write returns, so an acked write survives a crash with zero loss, and concurrent writers coalesce onto one shared fsync rather than paying one each. Neither mode is durability off."},
			{Code: "no-mvcc", Note: "this cell measures kv's bare hash-log storage core through its Set/Get/Delete surface, the durable peer to bbolt and badger's point path, not the full transactional shell."},
			{Code: "unordered", Note: "the index is a hash table with no key order, so there is no sorted scan; range and scan workloads (readseq, ycsb-e) do not apply and are skipped."},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	// Size the resident cold index to the cell's key count. The index is in memory and does not
	// grow, the F2-style design where the value bytes spill to disk but the key index does not,
	// so it must be at least the distinct-key count or writes past its capacity are dropped. The
	// harness passes the cardinality as a hint; a zero hint falls back to the package default.
	//
	// Size the resident window of the cold log to the harness's per-regime cache budget. That
	// budget is the working set in the cache-resident regime and a fraction of it out of cache,
	// the same budget pebble gets for its block cache and bbolt gets through the page cache, so
	// every engine is held to one memory ceiling. The whole budget goes to the ring; the read
	// cache is kept small because a resident ring already serves a cold read from memory, so a
	// second copy in the read cache would only spend RAM the budget did not grant.
	//
	// Size the hot tier to the value, not to a tiny-record assumption. One segment holds about
	// hotRecords records, so a segment is hotRecords * the framed record size, and its index is
	// sized to those records with headroom. A segment sized to the value seals a handful of times
	// over a fill instead of a dozen, and an index sized to the records it holds, rather than a
	// heuristic that assumes 32-byte records and over-allocates a million slots for 1 KiB values,
	// drops per-seal allocation by an order of magnitude.
	const hotRecords = 32768
	const maxHotBytes = 64 << 20       // cap so a large-value workload cannot balloon a segment
	recordBytes := cfg.ValueBytes + 32 // value plus key, length prefix, op byte, and log header
	if recordBytes <= 0 {
		recordBytes = 1056
	}
	hotBytes := min(int64(hotRecords)*int64(recordBytes), maxHotBytes)
	// FULL asks for durability on return, so open the engine in its per-commit mode: a write
	// appends to the cold log's group-commit flusher and does not return until the shared fsync
	// covers it. DEFAULT runs the background-commit path, which is still durable, just on a short
	// delay: the write acks from the hot tier and the flusher fsyncs it a moment later, the Redis
	// everysec contract. Two flush granularities, both durable, not on and off.
	syncWrites := cfg.Synchronous == "FULL"
	opts := hlog.Options{
		KeyCapacity:    int(cfg.Cardinality),
		HotBytes:       hotBytes,
		HotKeys:        hotRecords + hotRecords/4,
		ResidentBytes:  cfg.CacheBytes,
		ReadCacheCells: 4096,
		SyncWrites:     syncWrites,
	}
	db, err := hlog.Open(cfg.Dir+"/data.kv", opts)
	if err != nil {
		return err
	}
	e.db = db
	// In FULL each write is already on the platter when it returns, so the harness Flush step is a
	// no-op barrier; in DEFAULT the background group commit runs untouched and Flush is a no-op too.
	// The engine has no separate per-write fsync knob beyond the mode chosen at Open.
	e.sync = syncWrites
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	// A nil scratch makes Get allocate and return an owned copy, so the value stays valid after
	// the hot segment it came from is recycled and so concurrent readers never share a buffer.
	v, ok, err := e.db.Get(key, nil)
	if err != nil || !ok {
		return nil, false, err
	}
	return v, true, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	e.db.Set(key, value)
	return nil
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	e.db.Delete(key)
	return nil
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

// Scan is unsupported: the index is unordered. The driver skips scan workloads for kv
// because Caps.Ordered is false, so the empty iterator is belt and braces.
func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return emptyIter{}, nil
}

func (e *eng) Flush(_ context.Context) error {
	if !e.sync {
		return nil
	}
	return e.db.Sync()
}

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// batch applies its writes straight through to the engine on Commit. kv's hash-log core has no
// atomic multi-key commit, so this is a convenience grouping, not an atomic batch, which Caps
// states by leaving AtomicBatch false. Each Put and Delete is the same single-record append the
// point path uses.
type batch struct {
	e   *eng
	ops []op
}

type op struct {
	del  bool
	k, v []byte
}

func (b *batch) Put(k, v []byte) {
	b.ops = append(b.ops, op{k: append([]byte(nil), k...), v: append([]byte(nil), v...)})
}

func (b *batch) Delete(k []byte) {
	b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)})
}

func (b *batch) Len() int { return len(b.ops) }

func (b *batch) Commit(_ context.Context) error {
	for _, o := range b.ops {
		if o.del {
			b.e.db.Delete(o.k)
		} else {
			b.e.db.Set(o.k, o.v)
		}
	}
	b.ops = nil
	return nil
}

type emptyIter struct{}

func (emptyIter) Next() bool    { return false }
func (emptyIter) Key() []byte   { return nil }
func (emptyIter) Value() []byte { return nil }
func (emptyIter) Err() error    { return nil }
func (emptyIter) Close() error  { return nil }
