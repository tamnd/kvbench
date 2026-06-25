// Package kv adapts github.com/tamnd/kv, the single-file embedded Go store this
// whole benchmark exists to keep honest. kv ships two engine cores behind one
// API, so it registers twice: kv-btree (the in-place B+tree default, tuned for
// read latency) and kv-lsm (the log-structured core, tuned for write
// throughput). Both are pure Go with no cgo, and both write a single .kv file
// plus a WAL sidecar, so they sit in the same single-file class as bbolt and
// lmdb.
//
// There is no home-field advantage here. kv goes through the exact same Engine
// SPI as every other store and gets no special path in the driver.
package kv

import (
	"context"
	"errors"
	"path/filepath"

	tkv "github.com/tamnd/kv"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-btree", func() engine.Engine { return &eng{kind: tkv.BTree} })
	engine.Register("kv-lsm", func() engine.Engine { return &eng{kind: tkv.LSM} })
}

type eng struct {
	kind tkv.EngineKind
	db   *tkv.DB
}

func (e *eng) name() string {
	if e.kind == tkv.LSM {
		return "kv-lsm"
	}
	return "kv-btree"
}

func (e *eng) Meta() engine.Meta {
	fam := engine.FamilyBTree
	if e.kind == tkv.LSM {
		fam = engine.FamilyLSM
	}
	return engine.Meta{
		Name: e.name(), Family: fam, Mode: engine.ModeInProc,
		Version: "main",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "scan-overshoot", Note: "kv's Scan uses the zero-copy streaming cursor (NewScanCursor), which pulls entries in geometric batches (8, 16, 32, ... up to 256) lazily as the driver advances and stops once the scan closes, so a bounded scan resolves only the entries read plus at most the unread remainder of its last batch, not the whole tail. The scan numbers (readseq, ycsb-e) carry that small fixed-batch overshoot, no more. This supersedes the old eager-materialize behavior: kv materialized the full forward range at construction before it grew a streaming cursor, and any result file that still cites an eager-scan asterisk predates the switch."},
			{Code: "off-eq-full", Note: "kv cannot express durability OFF through its public API: WithSynchronous(SyncOff) sets Options.Sync to 0, which db.Options.sync() reads as unset and maps to SyncFull. So the OFF cell measures the same per-commit fsync path as FULL, not a no-fsync path. The fix belongs in kv (give SyncOff a non-zero value, or carry a 'set' flag); until then OFF and FULL are the same run."},
		},
	}
}

// sync maps the kvbench durability contract onto kv's WAL sync levels. NORMAL
// fsyncs at checkpoint and periodically (the WAL-mode default); FULL fsyncs every
// commit. OFF asks for SyncOff, but see the off-eq-full asterisk: kv currently
// folds SyncOff back into SyncFull because its value collides with the unset
// default, so the OFF and FULL cells exercise the same path until kv is fixed.
func syncLevel(s string) tkv.Sync {
	switch s {
	case "OFF":
		return tkv.SyncOff
	case "FULL":
		return tkv.SyncFull
	default:
		return tkv.SyncNormal
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	path := filepath.Join(cfg.Dir, "data.kv")
	opts := []tkv.Option{
		tkv.WithEngine(e.kind),
		tkv.WithSynchronous(syncLevel(cfg.Synchronous)),
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
	// Use kv's top-level Get, the engine's lightest point read: an owned-copy lookup at the latest
	// committed snapshot with no transaction to begin and discard and no snapshot watermark to
	// register. A single benchmark Get does not need snapshot isolation across keys, so the heavier
	// View transaction the harness used to wrap this only added per-op machinery that hid kv's real
	// point-read cost. This matches how the pebble adapter calls pebble's direct Get; bbolt rides a
	// View only because bbolt has no transaction-free read.
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

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	txn := e.db.Begin(false)
	// A forward scan from start never reads keys below it, so bound the cursor there. That is the
	// only bound the Scan signature gives us and it keeps kv from materializing the keyspace behind
	// the cursor. NewScanCursor is the zero-copy forward path: it hands back key and value views
	// into kv's internal storage instead of copying each one out, which is what the harness needs
	// since it reads each entry transiently and advances, the same way the bbolt adapter rides
	// bbolt's mmap cursor.
	sc, err := txn.NewScanCursor(tkv.IterOptions{Lower: start})
	if err != nil {
		txn.Discard()
		return nil, err
	}
	return &iter{txn: txn, sc: sc}, nil
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
	defer wb.Close()
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

// iter walks the kv zero-copy ScanCursor forward from start. The kvbench Iterator contract is "Next
// then read", which is exactly the cursor's drive: the first Next seeks to the lower bound and later
// ones step. Key and Value return the cursor's transient views, valid until the next Next, which is
// all the harness needs since it reads each entry before advancing.
type iter struct {
	txn *tkv.Txn
	sc  *tkv.ScanCursor
}

func (i *iter) Next() bool    { return i.sc.Next() }
func (i *iter) Key() []byte   { return i.sc.Key() }
func (i *iter) Value() []byte { return i.sc.Value() }
func (i *iter) Err() error    { return i.sc.Error() }
func (i *iter) Close() error {
	_ = i.sc.Close()
	i.txn.Discard()
	return nil
}
