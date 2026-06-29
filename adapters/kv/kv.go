// Package kv adapts github.com/tamnd/kv, the single-file embedded Go store this
// whole benchmark exists to keep honest. kv now ships a single core, f2: a
// latch-free sharded hash index over a self-durable hybrid log. It registers once,
// as kv, and goes through the full public DB stack (WAL, MVCC, transactions,
// checkpoint), so this cell measures what a user actually gets, not the bare core.
// The adapters/f2 cells measure the f2 core directly, without the host DB around
// it; the gap between the two is the cost of the durable, transactional shell.
//
// kv is pure Go with no cgo and writes a single .kv file plus a WAL sidecar, so it
// sits in the same single-file class as bbolt and lmdb. It is unordered: f2 is a
// hash index with no key order, so there is no scan, and range and scan workloads
// (readseq, ycsb-e) do not apply and are skipped.
//
// There is no home-field advantage here. kv goes through the exact same Engine SPI
// as every other store and gets no special path in the driver.
package kv

import (
	"context"
	"errors"
	"path/filepath"

	tkv "github.com/tamnd/kv"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv", func() engine.Engine { return &eng{} })
}

type eng struct {
	db *tkv.DB
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "kv", Family: engine.FamilyHashLog, Mode: engine.ModeInProc,
		Version: "main",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the DEFAULT profile opens kv as the library ships, which is SyncFull: an fsync on every commit. That is the honest out-of-box number and it is fsync-bound, so use --durability OFF to see the engine's write path without the per-commit barrier and --durability NORMAL for the checkpoint-and-timer mode."},
			{Code: "unordered", Note: "kv's f2 core is a hash index with no key order, so it has no scan; range and scan workloads (readseq, ycsb-e) do not apply and are skipped for it."},
		},
	}
}

// syncLevel maps the kvbench durability contract onto kv's WAL sync levels. OFF
// asks for SyncOff, kv's no-fsync path, so the OFF cell measures kv with the
// durability barrier removed, the same shape every other engine's OFF cell
// measures; NORMAL fdatasyncs at checkpoint and periodically; FULL fsyncs every
// commit. DEFAULT is handled by Open, which leaves the option off so kv uses its
// shipped default rather than a value this adapter picks.
func syncLevel(s string) tkv.Sync {
	switch s {
	case "OFF":
		return tkv.SyncOff
	case "NORMAL":
		return tkv.SyncNormal
	default:
		return tkv.SyncFull
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	path := filepath.Join(cfg.Dir, "data.kv")
	var opts []tkv.Option
	// DEFAULT means open the engine exactly as its library ships, so do not pin a
	// sync level at all and let kv use its own default (SyncFull). Any explicit dial
	// maps onto kv's matching WAL level.
	if cfg.Synchronous != "" && cfg.Synchronous != "DEFAULT" {
		opts = append(opts, tkv.WithSynchronous(syncLevel(cfg.Synchronous)))
	}
	if cfg.CacheBytes > 0 {
		opts = append(opts, tkv.WithCacheSize(int(cfg.CacheBytes)))
	}
	db, err := tkv.Open(path, opts...)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	// kv's top-level Get is the lightest point read: an owned-copy lookup at the
	// latest committed snapshot with no transaction to begin and discard. A single
	// benchmark Get does not need snapshot isolation across keys, so this matches how
	// the pebble adapter calls pebble's direct Get.
	v, err := e.db.Get(key)
	if errors.Is(err, tkv.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.db.Update(func(t *tkv.Txn) error { return t.Set(key, value) })
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.db.Update(func(t *tkv.Txn) error { return t.Delete(key) })
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

// Scan is unsupported: f2 is unordered. The driver skips scan workloads for kv
// because Caps.Ordered is false, so the empty iterator is belt and braces.
func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return emptyIter{}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Checkpoint() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// batch buffers writes and drives them through a single kv WriteBatch on Commit,
// which is the engine's atomic, memory-bounded bulk path.
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
	wb := b.e.db.NewWriteBatch(len(b.ops) + 1)
	defer func() { _ = wb.Close() }()
	for _, o := range b.ops {
		if o.del {
			if err := wb.Delete(o.k); err != nil {
				return err
			}
		} else if err := wb.Set(o.k, o.v); err != nil {
			return err
		}
	}
	b.ops = nil
	return wb.Flush()
}

type emptyIter struct{}

func (emptyIter) Next() bool    { return false }
func (emptyIter) Key() []byte   { return nil }
func (emptyIter) Value() []byte { return nil }
func (emptyIter) Err() error    { return nil }
func (emptyIter) Close() error  { return nil }
