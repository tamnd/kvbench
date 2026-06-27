// Package pebble adapts github.com/cockroachdb/pebble, the production-hardened
// pure-Go LSM.
package pebble

import (
	"context"

	"github.com/cockroachdb/pebble"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("pebble", func() engine.Engine { return &eng{} }) }

type eng struct {
	db *pebble.DB
	wo *pebble.WriteOptions
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "pebble", Family: engine.FamilyLSM, Mode: engine.ModeInProc,
		Version: "v1",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true,
			SingleFile: false, PureNoCgo: true,
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	opts := &pebble.Options{}
	if cfg.CacheBytes > 0 {
		opts.Cache = pebble.NewCache(cfg.CacheBytes)
	}
	db, err := pebble.Open(cfg.Dir, opts)
	if err != nil {
		return err
	}
	e.db = db
	// Match the suite-wide durability contract: only FULL fsyncs the WAL per
	// commit. OFF and NORMAL both leave the WAL to the OS, same as badger and
	// goleveldb, so the three levels mean the same thing across engines.
	e.wo = pebble.NoSync
	if cfg.Synchronous == "FULL" {
		e.wo = pebble.Sync
	}
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	v, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := append([]byte(nil), v...)
	closer.Close()
	return out, true, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error { return e.db.Set(key, value, e.wo) }
func (e *eng) Delete(_ context.Context, key []byte) error     { return e.db.Delete(key, e.wo) }

func (e *eng) NewBatch() engine.Batch { return &batch{e: e, b: e.db.NewBatch()} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	it, err := e.db.NewIter(&pebble.IterOptions{LowerBound: start})
	if err != nil {
		return nil, err
	}
	return &iter{it: it, first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Flush() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	m := e.db.Metrics()
	var written int64
	for _, lvl := range m.Levels {
		written += int64(lvl.BytesCompacted) + int64(lvl.BytesFlushed)
	}
	if written > 0 {
		s.BytesWritten = written
	}
	return s, nil
}

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

type batch struct {
	e *eng
	b *pebble.Batch
}

func (b *batch) Put(k, v []byte) { _ = b.b.Set(k, v, nil) }
func (b *batch) Delete(k []byte) { _ = b.b.Delete(k, nil) }
func (b *batch) Len() int        { return int(b.b.Count()) }
func (b *batch) Commit(_ context.Context) error {
	err := b.e.db.Apply(b.b, b.e.wo)
	b.b.Close()
	return err
}

type iter struct {
	it    *pebble.Iterator
	first bool
}

func (i *iter) Next() bool {
	if i.first {
		i.first = false
		return i.it.First()
	}
	return i.it.Next()
}
func (i *iter) Key() []byte   { return i.it.Key() }
func (i *iter) Value() []byte { return i.it.Value() }
func (i *iter) Err() error    { return i.it.Error() }
func (i *iter) Close() error  { return i.it.Close() }
