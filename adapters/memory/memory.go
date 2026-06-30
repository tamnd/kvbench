// Package memory is a non-durable in-memory reference engine: a "speed of
// light" ceiling, explicitly labeled non-durable. It is not a peer.
package memory

import (
	"bytes"
	"context"
	"sort"
	"sync"

	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("memory", func() engine.Engine { return &eng{} }) }

type eng struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "memory", Family: engine.FamilyInMemory, Mode: engine.ModeInProc, Reference: true,
		Version:   "builtin",
		Caps:      engine.Capabilities{Ordered: true, AtomicBatch: true, Durable: false, SingleFile: false, PureNoCgo: true},
		Asterisks: []engine.Asterisk{{Code: "non-durable", Note: "in-memory reference, no persistence; speed-of-light ceiling only"}},
	}
}

func (e *eng) Open(_ context.Context, _ engine.Config) error {
	e.m = make(map[string][]byte)
	return nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	e.mu.RLock()
	v, ok := e.m[string(key)]
	e.mu.RUnlock()
	return v, ok, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	e.mu.Lock()
	e.m[string(key)] = append([]byte(nil), value...)
	e.mu.Unlock()
	return nil
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	e.mu.Lock()
	delete(e.m, string(key))
	e.mu.Unlock()
	return nil
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	e.mu.RLock()
	keys := make([]string, 0, len(e.m))
	for k := range e.m {
		if bytes.Compare([]byte(k), start) >= 0 {
			keys = append(keys, k)
		}
	}
	e.mu.RUnlock()
	sort.Strings(keys)
	return &iter{e: e, keys: keys, idx: -1}, nil
}

func (e *eng) Flush(_ context.Context) error { return nil }

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error { e.m = nil; return nil }

type batch struct {
	e   *eng
	ops []op
}
type op struct {
	del bool
	k   []byte
	v   []byte
}

func (b *batch) Put(k, v []byte) {
	b.ops = append(b.ops, op{k: append([]byte(nil), k...), v: append([]byte(nil), v...)})
}
func (b *batch) Delete(k []byte) { b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)}) }
func (b *batch) Len() int        { return len(b.ops) }
func (b *batch) Commit(_ context.Context) error {
	b.e.mu.Lock()
	for _, o := range b.ops {
		if o.del {
			delete(b.e.m, string(o.k))
		} else {
			b.e.m[string(o.k)] = o.v
		}
	}
	b.e.mu.Unlock()
	b.ops = nil
	return nil
}

type iter struct {
	e    *eng
	keys []string
	idx  int
	k, v []byte
}

func (i *iter) Next() bool {
	i.idx++
	if i.idx >= len(i.keys) {
		return false
	}
	i.e.mu.RLock()
	i.k = []byte(i.keys[i.idx])
	i.v = i.e.m[i.keys[i.idx]]
	i.e.mu.RUnlock()
	return true
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error  { return nil }
