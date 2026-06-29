//go:build cgo_engines

// Package rocksdb adapts RocksDB, the Facebook/Meta LSM that grew out of
// LevelDB, via the linxGnu/grocksdb cgo binding. Unlike the LMDB and libmdbx
// bindings, grocksdb does not bundle the C++ source: it links the system
// librocksdb, so building this adapter needs librocksdb plus its compression
// libraries on the host (brew install rocksdb, or apt install librocksdb-dev).
// Built only with -tags cgo_engines.
//
// It is the production-grade LSM counterweight in the embedded class: where
// Pebble and goleveldb are pure-Go LSMs, RocksDB is the C++ original that most
// server databases embed, so it sets the bar the Go LSMs are measured against.
package rocksdb

import (
	"context"

	"github.com/linxGnu/grocksdb"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("rocksdb", func() engine.Engine { return &eng{} }) }

type eng struct {
	db   *grocksdb.DB
	opts *grocksdb.Options
	ro   *grocksdb.ReadOptions
	wo   *grocksdb.WriteOptions
	fo   *grocksdb.FlushOptions
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "rocksdb", Family: engine.FamilyLSM, Mode: engine.ModeCgo,
		Version: "system-lib",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: false,
			OnlineBackup: true, SingleFile: false, PureNoCgo: false,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default write does not fsync per commit (WriteOptions.Sync=false): the WAL is buffered to the OS and fsynced in the background, the same async-WAL class as pebble, badger and goleveldb"},
			{Code: "cgo-tax", Note: "reached via cgo into C++; the call-boundary cost is included, not subtracted"},
			{Code: "system-lib", Note: "links the host librocksdb rather than a bundled copy, so the measured version is whatever the host installed (brew rocksdb on macOS, librocksdb-dev on Debian/Ubuntu)"},
			{Code: "not-single-file", Note: "RocksDB is a directory of SST files plus a WAL, not a single file, so it is not in the single-file class with kv, bbolt and the LMDB pair"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	e.opts = grocksdb.NewDefaultOptions()
	e.opts.SetCreateIfMissing(true)
	e.ro = grocksdb.NewDefaultReadOptions()
	e.wo = grocksdb.NewDefaultWriteOptions()
	e.fo = grocksdb.NewDefaultFlushOptions()
	// Sync=true fsyncs the WAL on every write; false leaves it to the background
	// flush, RocksDB's shipped default and the async-WAL class of the Go LSMs.
	switch cfg.Synchronous {
	case "FULL":
		e.wo.SetSync(true)
	default: // DEFAULT, OFF, NORMAL: async WAL, no per-write fsync barrier
		e.wo.SetSync(false)
	}
	db, err := grocksdb.OpenDb(e.opts, cfg.Dir)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	s, err := e.db.Get(e.ro, key)
	if err != nil {
		return nil, false, err
	}
	defer s.Free()
	if !s.Exists() {
		return nil, false, nil
	}
	return append([]byte(nil), s.Data()...), true, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	return e.db.Put(e.wo, key, value)
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	return e.db.Delete(e.wo, key)
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e, wb: grocksdb.NewWriteBatch()} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	it := e.db.NewIterator(e.ro)
	if len(start) == 0 {
		it.SeekToFirst()
	} else {
		it.Seek(start)
	}
	return &iter{it: it, first: true}, nil
}

func (e *eng) Flush(_ context.Context) error { return e.db.Flush(e.fo) }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		e.db.Close()
	}
	if e.ro != nil {
		e.ro.Destroy()
	}
	if e.wo != nil {
		e.wo.Destroy()
	}
	if e.fo != nil {
		e.fo.Destroy()
	}
	if e.opts != nil {
		e.opts.Destroy()
	}
	return nil
}

type batch struct {
	e  *eng
	wb *grocksdb.WriteBatch
}

func (b *batch) Put(k, v []byte) { b.wb.Put(k, v) }
func (b *batch) Delete(k []byte) { b.wb.Delete(k) }
func (b *batch) Len() int        { return b.wb.Count() }
func (b *batch) Commit(_ context.Context) error {
	err := b.e.db.Write(b.e.wo, b.wb)
	b.wb.Clear()
	return err
}

type iter struct {
	it    *grocksdb.Iterator
	first bool
	k, v  []byte
}

func (i *iter) Next() bool {
	if !i.first {
		i.it.Next()
	}
	i.first = false
	if !i.it.Valid() {
		return false
	}
	k := i.it.Key()
	v := i.it.Value()
	i.k = append([]byte(nil), k.Data()...)
	i.v = append([]byte(nil), v.Data()...)
	k.Free()
	v.Free()
	return true
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return i.it.Err() }
func (i *iter) Close() error  { i.it.Close(); return nil }
