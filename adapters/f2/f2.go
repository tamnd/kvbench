// Package f2 adapts github.com/tamnd/kv/f2, the storage core kv ships: a
// latch-free sharded hash index over a per-shard hybrid log. It stores only an
// eight-byte atomic word per index slot (a tag plus a log offset), holding no key
// bytes resident, so the resident index is flat in key length. It registers as
// kv-f2 and goes through the same Engine SPI as every other store, so it gets no
// home-field path in the driver.
//
// This cell is the memory-only ceiling: every value stays in RAM and nothing is
// fsynced. Run it beside the inmem faster ceiling, the same store one design
// generation back that guards its log and index with a single RWMutex, to see the
// lock tax the latch-free path removes; the difference between the two is the
// coordination cost, not the data structure. It is unordered (Ordered:false, scan
// workloads skipped) and not durable in this profile (Durable:false, carries an
// asterisk).
package f2

import (
	"context"

	"github.com/tamnd/kv/f2"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-f2", func() engine.Engine { return &eng{} })
}

type eng struct {
	s *f2.Store
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "kv-f2", Family: engine.FamilyHashLog, Mode: engine.ModeInProc,
		Version: "main",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: false, Durable: false, Transactions: false,
			OnlineBackup: false, SingleFile: false, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "memory-only", Note: "kv-f2 is benchmarked in its full-resident profile: every value stays in RAM and nothing is fsynced, so this is an in-memory ceiling, not a durable store. It measures the cost of a compact-index hash probe and a log append while the resident index holds no key bytes, the read and write path the durable single-file layout preserves, not a crash-safe engine a user gets today."},
			{Code: "unordered", Note: "kv-f2 has no ordered scan; range and scan workloads do not apply and are skipped for it."},
		},
	}
}

func (e *eng) Open(_ context.Context, _ engine.Config) error {
	s, err := f2.New(f2.DefaultTunables())
	if err != nil {
		return err
	}
	e.s = s
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) { return e.s.Get(key) }
func (e *eng) Put(_ context.Context, key, value []byte) error          { return e.s.Set(key, value) }
func (e *eng) Delete(_ context.Context, key []byte) error              { return e.s.Delete(key) }
func (e *eng) NewBatch() engine.Batch                                  { return &batch{e: e} }

// Scan is unsupported: the engine is unordered. The driver skips scan workloads
// for it because Caps.Ordered is false, so the empty iterator is belt and braces.
func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) { return emptyIter{}, nil }

func (e *eng) Flush(_ context.Context) error                 { return nil }
func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }
func (e *eng) Close(_ context.Context) error {
	if e.s != nil {
		return e.s.Close()
	}
	return nil
}

// batch applies buffered writes one at a time; the engine has no atomic batch, so
// this is a convenience grouping, not a transaction.
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
func (b *batch) Delete(k []byte) {
	b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)})
}
func (b *batch) Len() int { return len(b.ops) }
func (b *batch) Commit(_ context.Context) error {
	for _, o := range b.ops {
		if o.del {
			if err := b.e.s.Delete(o.k); err != nil {
				return err
			}
		} else if err := b.e.s.Set(o.k, o.v); err != nil {
			return err
		}
	}
	b.ops = nil
	return nil
}

type emptyIter struct{}

func (emptyIter) Next() bool    { return false }
func (emptyIter) Key() []byte   { return nil }
func (emptyIter) Value() []byte { return nil }
func (emptyIter) Err() error    { return nil }
func (emptyIter) Close() error  { return nil }
