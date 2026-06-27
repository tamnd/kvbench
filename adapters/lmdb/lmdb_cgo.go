//go:build cgo_engines

// Package lmdb adapts LMDB, the C copy-on-write mmap B+tree, via the
// PowerDNS/lmdb-go binding which bundles the LMDB C source (so no system
// liblmdb is required, only a C compiler). Built only with -tags cgo_engines.
package lmdb

import (
	"context"

	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("lmdb", func() engine.Engine { return &eng{} }) }

type eng struct {
	env *lmdb.Env
	dbi lmdb.DBI
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "lmdb", Family: engine.FamilyCOWBTree, Mode: engine.ModeCgo,
		Version: "bundled-c",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: false,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default does a full sync on every commit (no MDB_NOSYNC and no NoMetaSync), so the out-of-box durability is fsync-per-commit like bbolt, the strongest in this field"},
			{Code: "cgo-tax", Note: "reached via cgo; the call-boundary cost is included, not subtracted"},
			{Code: "normal-nometasync", Note: "NORMAL maps to LMDB NoMetaSync: data pages are fsynced per commit, the meta page is not, so it is more durable than the no-fsync LSMs and less than a full sync"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	env, err := lmdb.NewEnv()
	if err != nil {
		return err
	}
	if err := env.SetMaxDBs(1); err != nil {
		return err
	}
	// generous map size: enough for the loaded dataset plus COW headroom.
	mapSize := int64(cfg.ValueBytes+64) * 8_000_000
	if mapSize < (1 << 30) {
		mapSize = 1 << 30
	}
	if err := env.SetMapSize(mapSize); err != nil {
		return err
	}
	flags := uint(lmdb.NoReadahead)
	if cfg.Synchronous == "OFF" {
		flags |= lmdb.NoSync
	} else if cfg.Synchronous == "NORMAL" {
		flags |= lmdb.NoMetaSync
	}
	if err := env.Open(cfg.Dir, flags, 0o644); err != nil {
		return err
	}
	e.env = env
	return env.Update(func(txn *lmdb.Txn) error {
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
	err := e.env.View(func(txn *lmdb.Txn) error {
		txn.RawRead = true
		v, err := txn.Get(e.dbi, key)
		if lmdb.IsNotFound(err) {
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
	return e.env.Update(func(txn *lmdb.Txn) error { return txn.Put(e.dbi, key, value, 0) })
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.env.Update(func(txn *lmdb.Txn) error {
		err := txn.Del(e.dbi, key, nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	txn, err := e.env.BeginTxn(nil, lmdb.Readonly)
	if err != nil {
		return nil, err
	}
	txn.RawRead = true
	cur, err := txn.OpenCursor(e.dbi)
	if err != nil {
		txn.Abort()
		return nil, err
	}
	return &iter{txn: txn, cur: cur, start: append([]byte(nil), start...), first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.env.Sync(true) }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	if info, err := e.env.Info(); err == nil {
		s.OnDiskBytes = -1
		_ = info
	}
	return s, nil
}

func (e *eng) Close(_ context.Context) error {
	if e.env != nil {
		return e.env.Close()
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
	err := b.e.env.Update(func(txn *lmdb.Txn) error {
		for _, o := range b.ops {
			if o.del {
				if e := txn.Del(b.e.dbi, o.k, nil); e != nil && !lmdb.IsNotFound(e) {
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
	txn   *lmdb.Txn
	cur   *lmdb.Cursor
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
		i.k, i.v, err = i.cur.Get(i.start, nil, lmdb.SetRange)
	} else {
		i.k, i.v, err = i.cur.Get(nil, nil, lmdb.Next)
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
