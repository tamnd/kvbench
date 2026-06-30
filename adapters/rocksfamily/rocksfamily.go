//go:build cgo_engines || speedb_engines

// Package rocksfamily is the shared engine.Engine implementation for the
// RocksDB-API family, driven through the linxGnu/grocksdb cgo binding. RocksDB
// and Speedb share the same C API and ABI (Speedb is a drop-in fork), so they
// share this one adapter and differ only in the name they register under and the
// librocksdb the binary links. The rocksdb adapter builds under cgo_engines; the
// speedb adapter builds under speedb_engines, and the two are mutually exclusive
// in one binary because grocksdb links exactly one librocksdb.
//
// grocksdb does not bundle the C++ source: it links the host librocksdb, so a
// build needs librocksdb (or Speedb's drop-in build of it) plus the compression
// libraries on the host. See the adapter packages and the README for how each is
// provisioned.
package rocksfamily

import (
	"context"

	"github.com/linxGnu/grocksdb"
	"github.com/tamnd/kvbench/engine"
)

// New returns a grocksdb-backed engine registered under name, reporting version
// and carrying extra as library-specific asterisks on top of the family ones.
func New(name, version string, extra []engine.Asterisk) engine.Engine {
	return &eng{name: name, version: version, extra: extra}
}

type eng struct {
	name    string
	version string
	extra   []engine.Asterisk

	db   *grocksdb.DB
	opts *grocksdb.Options
	ro   *grocksdb.ReadOptions
	wo   *grocksdb.WriteOptions
	fo   *grocksdb.FlushOptions
}

func (e *eng) Meta() engine.Meta {
	// The family asterisks hold for every RocksDB-API engine; the adapter adds the
	// library-specific ones (which librocksdb is linked, the version source).
	as := []engine.Asterisk{
		{Code: "default-durability", Note: "the default write does not fsync per commit (WriteOptions.Sync=false): the WAL is buffered to the OS and fsynced in the background, the same async-WAL class as pebble, badger and goleveldb"},
		{Code: "cgo-tax", Note: "reached via cgo into C++; the call-boundary cost is included, not subtracted"},
		{Code: "not-single-file", Note: "a RocksDB-API store is a directory of SST files plus a WAL, not a single file, so it is not in the single-file class with kv, bbolt and the LMDB pair"},
	}
	as = append(as, e.extra...)
	return engine.Meta{
		Name: e.name, Family: engine.FamilyLSM, Mode: engine.ModeCgo,
		Class:   engine.ClassEmbedded,
		Version: e.version,
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: false,
			OnlineBackup: true, SingleFile: false, PureNoCgo: false,
		},
		Asterisks: as,
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	e.opts = grocksdb.NewDefaultOptions()
	e.opts.SetCreateIfMissing(true)
	e.ro = grocksdb.NewDefaultReadOptions()
	e.wo = grocksdb.NewDefaultWriteOptions()
	e.fo = grocksdb.NewDefaultFlushOptions()
	// Sync=true fsyncs the WAL on every write; false leaves it to the background
	// flush, the shipped default and the async-WAL class of the Go LSMs.
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
