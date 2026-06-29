//go:build cgo_engines

// Package libmdbx adapts libmdbx, the C copy-on-write mmap B+tree that descends
// from LMDB with a reworked page reclaim, larger limits, and a stronger no-fsync
// mode (SafeNoSync, which stays crash-consistent where LMDB's async map does not).
// It is reached through the erigontech/mdbx-go binding, which bundles the libmdbx C
// source, so no system library is required, only a C compiler, the same property
// the LMDB adapter relies on. Built only with -tags cgo_engines.
//
// It opens a single file (NoSubdir) so it sits in the same single-file class as
// LMDB and bbolt, and it is the ordered, transactional B+tree counterweight to kv's
// unordered hash-log core in the embedded class.
package libmdbx

import (
	"context"
	"path/filepath"

	"github.com/erigontech/mdbx-go/mdbx"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("libmdbx", func() engine.Engine { return &eng{} }) }

type eng struct {
	env *mdbx.Env
	dbi mdbx.DBI
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "libmdbx", Family: engine.FamilyCOWBTree, Mode: engine.ModeCgo,
		Version: "bundled-c",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: false,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default opens SYNC_DURABLE, a full fsync on every commit, so the out-of-box durability is fsync-per-commit like LMDB and bbolt, the strongest in this field"},
			{Code: "cgo-tax", Note: "reached via cgo; the call-boundary cost is included, not subtracted"},
			{Code: "normal-nometasync", Note: "NORMAL maps to MDBX_NOMETASYNC: data pages are fsynced per commit, the meta page is not, the same mapping the LMDB adapter uses. OFF maps to MDBX_UTTERLY_NOSYNC, the barrier-free mode that mirrors LMDB's MDB_NOSYNC, so the OFF write-path numbers compare like for like"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	env, err := mdbx.NewEnv(mdbx.Default)
	if err != nil {
		return err
	}
	// One named DBI (the root), like the LMDB adapter. Must be set before Open.
	if err := env.SetOption(mdbx.OptMaxDB, 1); err != nil {
		env.Close()
		return err
	}
	// Generous upper bound: enough for the loaded dataset plus COW headroom. Only the
	// upper geometry bound is pinned; the rest keep libmdbx's defaults (-1).
	upper := (cfg.ValueBytes + 64) * 8_000_000
	if upper < (1 << 30) {
		upper = 1 << 30
	}
	if err := env.SetGeometry(-1, -1, upper, -1, -1, -1); err != nil {
		env.Close()
		return err
	}
	flags := uint(mdbx.NoSubdir | mdbx.NoReadahead)
	switch cfg.Synchronous {
	case "OFF":
		// Barrier-free, the mirror of LMDB's MDB_NOSYNC, so the OFF write-path
		// comparison is like for like. It can lose or corrupt the last writes on a
		// crash, which is exactly what OFF means.
		flags |= mdbx.UtterlyNoSync
	case "NORMAL":
		flags |= mdbx.NoMetaSync
	default:
		flags |= mdbx.Durable
	}
	if err := env.Open(filepath.Join(cfg.Dir, "data.mdbx"), flags, 0o644); err != nil {
		env.Close()
		return err
	}
	e.env = env
	return env.Update(func(txn *mdbx.Txn) error {
		dbi, err := txn.OpenRoot(0)
		if err != nil {
			return err
		}
		e.dbi = dbi
		return nil
	})
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	var out []byte
	var found bool
	err := e.env.View(func(txn *mdbx.Txn) error {
		v, err := txn.Get(e.dbi, key)
		if mdbx.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		out = append([]byte(nil), v...)
		return nil
	})
	return out, found, err
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.env.Update(func(txn *mdbx.Txn) error { return txn.Put(e.dbi, key, value, 0) })
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.env.Update(func(txn *mdbx.Txn) error {
		err := txn.Del(e.dbi, key, nil)
		if mdbx.IsNotFound(err) {
			return nil
		}
		return err
	})
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	txn, err := e.env.BeginTxn(nil, mdbx.Readonly)
	if err != nil {
		return nil, err
	}
	cur, err := txn.OpenCursor(e.dbi)
	if err != nil {
		txn.Abort()
		return nil, err
	}
	return &iter{txn: txn, cur: cur, start: append([]byte(nil), start...), first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.env.Sync(true, false) }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.env != nil {
		e.env.Close()
	}
	return nil
}

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
func (b *batch) Delete(k []byte) { b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)}) }
func (b *batch) Len() int        { return len(b.ops) }
func (b *batch) Commit(_ context.Context) error {
	err := b.e.env.Update(func(txn *mdbx.Txn) error {
		for _, o := range b.ops {
			if o.del {
				if e := txn.Del(b.e.dbi, o.k, nil); e != nil && !mdbx.IsNotFound(e) {
					return e
				}
			} else if e := txn.Put(b.e.dbi, o.k, o.v, 0); e != nil {
				return e
			}
		}
		return nil
	})
	b.ops = nil
	return err
}

type iter struct {
	txn   *mdbx.Txn
	cur   *mdbx.Cursor
	start []byte
	first bool
	k, v  []byte
	done  bool
}

func (i *iter) Next() bool {
	if i.done {
		return false
	}
	var err error
	if i.first {
		i.first = false
		i.k, i.v, err = i.cur.Get(i.start, nil, mdbx.SetRange)
	} else {
		i.k, i.v, err = i.cur.Get(nil, nil, mdbx.Next)
	}
	if err != nil {
		i.done = true
		return false
	}
	return true
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error  { i.cur.Close(); i.txn.Abort(); return nil }
