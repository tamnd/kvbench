// Package pogreb adapts github.com/akrylysov/pogreb, a pure-Go embedded
// key/value store with a hash-table index. It is fast at point operations and
// crash-resistant, but it is unordered: there is no sorted iteration, so the
// scan workloads report an honest "unordered" error rather than a fake number.
package pogreb

import (
	"context"
	"errors"

	"github.com/akrylysov/pogreb"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("pogreb", func() engine.Engine { return &eng{} }) }

type eng struct {
	db   *pogreb.DB
	sync bool
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "pogreb", Family: engine.FamilyHashLog, Mode: engine.ModeInProc,
		Version: "v0.10",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: false, Durable: true,
			SingleFile: false, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default fsyncs the log on a background interval, not per put, so the out-of-box write number reflects deferred durability"},
			{Code: "unordered", Note: "hash index, no sorted iteration; scan workloads are not supported and report an error instead of a number"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	opts := &pogreb.Options{}
	// pogreb's background sync interval is its durability knob. FULL forces a
	// sync on every write through Flush in the batch path; OFF lets the OS flush.
	e.sync = cfg.Synchronous == "FULL"
	db, err := pogreb.Open(cfg.Dir, opts)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	v, err := e.db.Get(key)
	if err != nil {
		return nil, false, err
	}
	return v, v != nil, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	if err := e.db.Put(key, value); err != nil {
		return err
	}
	if e.sync {
		return e.db.Sync()
	}
	return nil
}

func (e *eng) Delete(_ context.Context, key []byte) error { return e.db.Delete(key) }

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return nil, errors.New("pogreb is unordered: scan/range iteration is not supported")
}

func (e *eng) Flush(_ context.Context) error { return e.db.Sync() }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) {
	s := engine.UnknownStats()
	if sz, err := e.db.FileSize(); err == nil {
		s.OnDiskBytes = sz
	}
	return s, nil
}

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// pogreb has no native multi-op batch, so this applies puts/deletes one at a
// time and syncs once at the end when durability is FULL.
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
	for _, o := range b.ops {
		if o.del {
			if err := b.e.db.Delete(o.k); err != nil {
				return err
			}
		} else if err := b.e.db.Put(o.k, o.v); err != nil {
			return err
		}
	}
	b.ops = nil
	if b.e.sync {
		return b.e.db.Sync()
	}
	return nil
}
