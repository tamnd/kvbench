// Package goleveldb adapts github.com/syndtr/goleveldb, the widely-deployed
// pure-Go LevelDB port.
package goleveldb

import (
	"context"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("goleveldb", func() engine.Engine { return &eng{} }) }

type eng struct {
	db *leveldb.DB
	wo *opt.WriteOptions
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "goleveldb", Family: engine.FamilyLSM, Mode: engine.ModeInProc,
		Version: "v1.0",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true,
			SingleFile: false, PureNoCgo: true,
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	o := &opt.Options{}
	if cfg.CacheBytes > 0 {
		o.BlockCacheCapacity = int(cfg.CacheBytes)
	}
	db, err := leveldb.OpenFile(cfg.Dir, o)
	if err != nil {
		return err
	}
	e.db = db
	e.wo = &opt.WriteOptions{Sync: cfg.Synchronous == "FULL"}
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	v, err := e.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, false, nil
	}
	return v, err == nil, err
}

func (e *eng) Put(_ context.Context, key, value []byte) error { return e.db.Put(key, value, e.wo) }
func (e *eng) Delete(_ context.Context, key []byte) error     { return e.db.Delete(key, e.wo) }

func (e *eng) NewBatch() engine.Batch { return &batch{e: e, b: new(leveldb.Batch)} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	it := e.db.NewIterator(&util.Range{Start: start}, nil)
	return &iter{it: it, first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return nil }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	if v, err := e.db.GetProperty("leveldb.iostats"); err == nil {
		_ = v
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
	b *leveldb.Batch
}

func (b *batch) Put(k, v []byte) { b.b.Put(k, v) }
func (b *batch) Delete(k []byte) { b.b.Delete(k) }
func (b *batch) Len() int        { return b.b.Len() }
func (b *batch) Commit(_ context.Context) error {
	err := b.e.db.Write(b.b, b.e.wo)
	b.b.Reset()
	return err
}

type iter struct {
	it interface {
		Next() bool
		Key() []byte
		Value() []byte
		Error() error
		Release()
		First() bool
	}
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
func (i *iter) Close() error  { i.it.Release(); return nil }
