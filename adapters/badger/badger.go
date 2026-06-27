// Package badger adapts github.com/dgraph-io/badger/v4, the pure-Go LSM with
// WiscKey key/value separation.
package badger

import (
	"context"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("badger", func() engine.Engine { return &eng{} }) }

type eng struct {
	db   *badger.DB
	sync bool
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "badger", Family: engine.FamilyLSM, Mode: engine.ModeInProc,
		Version: "v4",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			SingleFile: false, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{{Code: "default-durability", Note: "DefaultOptions ship SyncWrites=false: the default writes to the value log and fsyncs in the background, not per commit, so its out-of-box write number stays fast"}},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	opts := badger.DefaultOptions(cfg.Dir).WithLoggingLevel(badger.ERROR)
	e.sync = cfg.Synchronous == "FULL"
	opts = opts.WithSyncWrites(e.sync)
	db, err := badger.Open(opts)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	var out []byte
	var found bool
	err := e.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		out, err = item.ValueCopy(nil)
		return err
	})
	return out, found, err
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.db.Update(func(txn *badger.Txn) error { return txn.Set(key, value) })
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.db.Update(func(txn *badger.Txn) error { return txn.Delete(key) })
}

func (e *eng) NewBatch() engine.Batch { return &batch{wb: e.db.NewWriteBatch()} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	txn := e.db.NewTransaction(false)
	opts := badger.DefaultIteratorOptions
	it := txn.NewIterator(opts)
	return &iter{txn: txn, it: it, start: start, first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Sync() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	lsm, vlog := e.db.Size()
	s.OnDiskBytes = lsm + vlog
	return s, nil
}

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

type batch struct {
	wb *badger.WriteBatch
	n  int
}

func (b *batch) Put(k, v []byte) {
	_ = b.wb.Set(append([]byte(nil), k...), append([]byte(nil), v...))
	b.n++
}
func (b *batch) Delete(k []byte) { _ = b.wb.Delete(append([]byte(nil), k...)); b.n++ }
func (b *batch) Len() int        { return b.n }
func (b *batch) Commit(_ context.Context) error {
	err := b.wb.Flush()
	b.n = 0
	return err
}

type iter struct {
	txn   *badger.Txn
	it    *badger.Iterator
	start []byte
	first bool
	k, v  []byte
}

func (i *iter) Next() bool {
	if i.first {
		i.first = false
		i.it.Seek(i.start)
	} else {
		i.it.Next()
	}
	if !i.it.Valid() {
		return false
	}
	item := i.it.Item()
	i.k = item.KeyCopy(nil)
	i.v, _ = item.ValueCopy(nil)
	return true
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error  { i.it.Close(); i.txn.Discard(); return nil }
