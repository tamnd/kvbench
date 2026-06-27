// Package bbolt adapts go.etcd.io/bbolt, the pure-Go copy-on-write B+tree and
// kv's closest peer in single-file feel.
package bbolt

import (
	"context"
	"path/filepath"

	"github.com/tamnd/kvbench/engine"
	bolt "go.etcd.io/bbolt"
)

func init() { engine.Register("bbolt", func() engine.Engine { return &eng{} }) }

var bucket = []byte("kvbench")

type eng struct {
	db *bolt.DB
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "bbolt", Family: engine.FamilyCOWBTree, Mode: engine.ModeInProc,
		Version: "v1.3",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default fsyncs the data file on every commit; bbolt has no async mode, so its out-of-box durability is the strongest and the slowest on writes in this field"},
			{Code: "normal-is-full", Note: "bbolt fsyncs every commit and has no periodic-flush mode, so NORMAL durability behaves exactly like FULL here"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	opts := &bolt.Options{}
	if cfg.Synchronous == "OFF" {
		opts.NoSync = true
	}
	db, err := bolt.Open(filepath.Join(cfg.Dir, "data.db"), 0o644, opts)
	if err != nil {
		return err
	}
	e.db = db
	return db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	var out []byte
	var found bool
	err := e.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucket).Get(key)
		if v != nil {
			out = append([]byte(nil), v...)
			found = true
		}
		return nil
	})
	return out, found, err
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put(key, value)
	})
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Delete(key)
	})
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	tx, err := e.db.Begin(false)
	if err != nil {
		return nil, err
	}
	c := tx.Bucket(bucket).Cursor()
	return &iter{tx: tx, c: c, start: start, first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Sync() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	st := e.db.Stats()
	s.NumFiles = 1
	_ = st
	return s, nil
}

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
	err := b.e.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket(bucket)
		for _, o := range b.ops {
			if o.del {
				if e := bk.Delete(o.k); e != nil {
					return e
				}
			} else if e := bk.Put(o.k, o.v); e != nil {
				return e
			}
		}
		return nil
	})
	b.ops = nil
	return err
}

type iter struct {
	tx    *bolt.Tx
	c     *bolt.Cursor
	start []byte
	first bool
	k, v  []byte
}

func (i *iter) Next() bool {
	if i.first {
		i.first = false
		i.k, i.v = i.c.Seek(i.start)
	} else {
		i.k, i.v = i.c.Next()
	}
	return i.k != nil
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error  { return i.tx.Rollback() }
