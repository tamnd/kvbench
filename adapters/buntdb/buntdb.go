// Package buntdb adapts github.com/tidwall/buntdb, a pure-Go ordered key/value
// store that keeps a btree index in memory and persists through an append-only
// file. Reads run at memory speed; the durability knob controls how often the
// AOF is fsynced.
package buntdb

import (
	"context"
	"path/filepath"

	"github.com/tamnd/kvbench/engine"
	"github.com/tidwall/buntdb"
)

func init() { engine.Register("buntdb", func() engine.Engine { return &eng{} }) }

type eng struct {
	db *buntdb.DB
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "buntdb", Family: engine.FamilyBTree, Mode: engine.ModeInProc,
		Version: "v1",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default SyncPolicy is EverySecond: the append-only file is fsynced about once a second, not on every commit, so the out-of-box write number is the deferred-sync path"},
			{Code: "in-mem-index", Note: "the whole dataset lives in memory with an append-only file for durability, so reads are RAM-speed and space is measured on the AOF"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	db, err := buntdb.Open(filepath.Join(cfg.Dir, "data.db"))
	if err != nil {
		return err
	}
	cf := buntdb.Config{}
	if err := db.ReadConfig(&cf); err == nil {
		switch cfg.Synchronous {
		case "OFF":
			cf.SyncPolicy = buntdb.Never
		case "FULL":
			cf.SyncPolicy = buntdb.Always
		default:
			cf.SyncPolicy = buntdb.EverySecond
		}
		_ = db.SetConfig(cf)
	}
	e.db = db
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	var out []byte
	var found bool
	err := e.db.View(func(tx *buntdb.Tx) error {
		v, err := tx.Get(string(key))
		if err == buntdb.ErrNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		out = []byte(v)
		return nil
	})
	return out, found, err
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.db.Update(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(string(key), string(value), nil)
		return err
	})
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.db.Update(func(tx *buntdb.Tx) error {
		_, err := tx.Delete(string(key))
		if err == buntdb.ErrNotFound {
			return nil
		}
		return err
	})
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	it := &iter{
		ch:   make(chan kv, 64),
		done: make(chan struct{}),
	}
	go func() {
		defer close(it.ch)
		_ = e.db.View(func(tx *buntdb.Tx) error {
			return tx.AscendGreaterOrEqual("", string(start), func(k, v string) bool {
				select {
				case it.ch <- kv{[]byte(k), []byte(v)}:
					return true
				case <-it.done:
					return false
				}
			})
		})
	}()
	return it, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Shrink() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
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
	err := b.e.db.Update(func(tx *buntdb.Tx) error {
		for _, o := range b.ops {
			if o.del {
				if _, e := tx.Delete(string(o.k)); e != nil && e != buntdb.ErrNotFound {
					return e
				}
			} else if _, _, e := tx.Set(string(o.k), string(o.v), nil); e != nil {
				return e
			}
		}
		return nil
	})
	b.ops = nil
	return err
}

type kv struct{ k, v []byte }

type iter struct {
	ch   chan kv
	done chan struct{}
	cur  kv
}

func (i *iter) Next() bool {
	v, ok := <-i.ch
	if !ok {
		return false
	}
	i.cur = v
	return true
}
func (i *iter) Key() []byte   { return i.cur.k }
func (i *iter) Value() []byte { return i.cur.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error {
	close(i.done)
	// drain so the producer goroutine can exit even if it is mid-send.
	for range i.ch {
	}
	return nil
}
