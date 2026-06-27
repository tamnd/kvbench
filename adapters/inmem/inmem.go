// Package inmem holds the in-memory floor and ceiling engines: the structures
// that bound what any durable engine in this suite could ever reach on a point
// workload, plus a do-nothing floor that isolates the harness and dispatch cost
// from the store cost. None of them persist, so none are peers to the durable
// engines; they exist to answer two questions the durable numbers cannot answer
// on their own. The floor (devnull) answers "how much of a cell's time is the
// harness, not the store". The ceilings (faster, otter, swiss) answer "what is
// the fastest a bare in-memory structure of this shape serves the same keys",
// which is the budget every layer the real engine carries above a bare map is
// spending.
//
// The ceilings are deliberately thin sketches of well-known designs rather than
// faithful ports: faster is a FASTER/Garnet-style append-only log behind an
// open-addressing hash index, otter is a sharded map in the shape otter's
// per-shard cache uses, and swiss is a single open-addressing table in the
// Swiss-table probe shape. Each is guarded so the driver's concurrent clients
// are safe; read the conc=1 cell for the single-thread structure ceiling and
// the conc=N cell for how the guard scales. The reads hand back a view into the
// structure rather than a copy because the driver discards the value, so these
// numbers are the zero-copy read ceiling.
package inmem

import (
	"context"
	"encoding/binary"
	"math/bits"
	"sync"

	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("devnull", func() engine.Engine { return &devnull{} })
	engine.Register("faster", func() engine.Engine { return newFaster() })
	engine.Register("otter", func() engine.Engine { return newOtter() })
	engine.Register("swiss", func() engine.Engine { return newSwiss() })
}

// hash64 is a small wyhash-style mix, enough to spread keys across probe slots
// and shards without pulling in a hashing dependency. The folded multiply
// (mul, then xor of the two halves) is the wyhash mixing primitive; the final
// murmur3 avalanche is what makes the low bits usable, which matters because the
// open-addressing tables index a slot with hash & mask and a weak low half would
// pile keys into a few slots and turn the linear probe into an O(n) walk.
func hash64(b []byte) uint64 {
	const (
		k0 = 0xff51afd7ed558ccd
		k1 = 0xc4ceb9fe1a85ec53
	)
	mix := func(a, c uint64) uint64 { hi, lo := bits.Mul64(a, c); return hi ^ lo }
	h := uint64(len(b))*k0 ^ k1
	for len(b) >= 8 {
		h = mix(h^binary.LittleEndian.Uint64(b), k0)
		b = b[8:]
	}
	var tail uint64
	for i := 0; i < len(b); i++ {
		tail |= uint64(b[i]) << (8 * uint(i))
	}
	h = mix(h^tail, k1)
	// murmur3 finalizer: avalanche so every input bit reaches the low bits.
	h ^= h >> 33
	h *= k0
	h ^= h >> 29
	h *= k1
	h ^= h >> 32
	return h
}

// emptyIter is the iterator the unordered ceilings hand back. The driver only
// asks for a scan on an ordered engine (RunCell skips a scan workload when the
// engine is unordered), so these engines never see a scan in a measured cell;
// the iterator exists so the SPI call is total rather than a panic.
type emptyIter struct{}

func (emptyIter) Next() bool    { return false }
func (emptyIter) Key() []byte   { return nil }
func (emptyIter) Value() []byte { return nil }
func (emptyIter) Err() error    { return nil }
func (emptyIter) Close() error  { return nil }

// ---- devnull: the do-nothing floor ----

// devnull implements every operation as the cheapest legal no-op: Put drops the
// write, Get reports the key absent, Scan returns an exhausted cursor. Its cell
// is the harness and dispatch floor, the time a cell spends in the workload
// generator, the op switch, the interface call, and the latency record before
// any store code runs. Subtract it from a real engine's cell to see the store's
// own share.
type devnull struct{}

func (*devnull) Meta() engine.Meta {
	return engine.Meta{
		Name: "devnull", Family: engine.FamilyInMemory, Mode: engine.ModeInProc,
		Version: "builtin",
		Caps:    engine.Capabilities{PureNoCgo: true},
		Asterisks: []engine.Asterisk{
			{Code: "no-op", Note: "devnull stores nothing and reads nothing back; its cell is the harness and dispatch floor, not a store. Reads always report not-found and writes are dropped, so any read-heavy cell here measures the cost of generating the op and dispatching it, the overhead every real engine's number also carries."},
		},
	}
}

func (*devnull) Open(context.Context, engine.Config) error             { return nil }
func (*devnull) Get(context.Context, []byte) ([]byte, bool, error)     { return nil, false, nil }
func (*devnull) Put(context.Context, []byte, []byte) error             { return nil }
func (*devnull) Delete(context.Context, []byte) error                  { return nil }
func (*devnull) Scan(context.Context, []byte) (engine.Iterator, error) { return emptyIter{}, nil }
func (*devnull) Flush(context.Context) error                           { return nil }
func (*devnull) Stats(context.Context) (engine.Stats, error)           { return engine.UnknownStats(), nil }
func (*devnull) Close(context.Context) error                           { return nil }
func (d *devnull) NewBatch() engine.Batch                              { return &nullBatch{} }

// nullBatch counts its ops so the loader's size-based flush still fires, but
// commits nothing.
type nullBatch struct{ n int }

func (b *nullBatch) Put([]byte, []byte)           { b.n++ }
func (b *nullBatch) Delete([]byte)                { b.n++ }
func (b *nullBatch) Len() int                     { return b.n }
func (b *nullBatch) Commit(context.Context) error { b.n = 0; return nil }

// ---- faster: append-log + open-addressing hash index ----

// faster sketches the FASTER/Garnet shape: values live in one append-only log
// and an open-addressing hash index maps a key's hash to the offset of its
// newest record. A Put appends a record and points the index slot at it, so an
// overwrite never moves an old value, it just strands it; a Get hashes the key,
// probes the index to the live offset, and returns a view into the log. The
// real FASTER protects the log with epochs so readers run lock-free against a
// log that a separate thread is trimming; this sketch trims nothing and guards
// the whole structure with one RWMutex, so the conc=1 cell reads the log-plus-
// index point cost and the conc=N cell reads how a single lock scales it.
//
// A slot is empty, full, or a tombstone. A delete turns a full slot into a
// tombstone rather than clearing it, because clearing would create a gap that
// ends a later key's probe run early and lose it; the probe walks past
// tombstones and only an empty slot ends a search. Tombstones are swept on the
// next grow.
type faster struct {
	mu   sync.RWMutex
	log  []byte     // append-only record log: each record is varint(keyLen) key varint(valLen) val
	idx  []fastSlot // open-addressing index, power-of-two length
	live int        // full slots
	used int        // full + tombstone slots, the load-factor driver
}

type slotState uint8

const (
	stEmpty slotState = iota
	stFull
	stTomb
)

type fastSlot struct {
	hash  uint64
	off   uint64 // record offset, valid when state == stFull
	state slotState
}

const fasterMaxLoad = 0.7

func newFaster() *faster {
	return &faster{
		log: make([]byte, 0, 1<<20),
		idx: make([]fastSlot, 1<<10),
	}
}

func (e *faster) Meta() engine.Meta {
	return engine.Meta{
		Name: "faster", Family: engine.FamilyInMemory, Mode: engine.ModeInProc,
		Version: "builtin",
		Caps:    engine.Capabilities{AtomicBatch: true, PureNoCgo: true},
		Asterisks: []engine.Asterisk{
			{Code: "non-durable", Note: "in-memory FASTER/Garnet-shaped ceiling: append-only value log behind an open-addressing hash index, no persistence and no log trimming. Unordered, so it serves point cells only. Reads return a view into the log, the zero-copy read ceiling; the conc=1 cell is the single-thread structure cost and conc=N shows the single guard's scaling."},
		},
	}
}

func (e *faster) Open(context.Context, engine.Config) error { return nil }

func (e *faster) appendRecord(key, val []byte) uint64 {
	off := uint64(len(e.log))
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(key)))
	e.log = append(e.log, tmp[:n]...)
	e.log = append(e.log, key...)
	n = binary.PutUvarint(tmp[:], uint64(len(val)))
	e.log = append(e.log, tmp[:n]...)
	e.log = append(e.log, val...)
	return off
}

// recordAt returns views of the key and value of the record at off.
func (e *faster) recordAt(off uint64) (key, val []byte) {
	b := e.log[off:]
	kl, n := binary.Uvarint(b)
	b = b[n:]
	key = b[:kl]
	b = b[kl:]
	vl, n := binary.Uvarint(b)
	b = b[n:]
	val = b[:vl]
	return key, val
}

func (e *faster) putLocked(key, val []byte) {
	if float64(e.used+1) > fasterMaxLoad*float64(len(e.idx)) {
		e.grow()
	}
	h := hash64(key)
	mask := uint64(len(e.idx) - 1)
	firstTomb := -1
	for i := h & mask; ; i = (i + 1) & mask {
		s := &e.idx[i]
		switch s.state {
		case stEmpty:
			off := e.appendRecord(key, val)
			if firstTomb >= 0 {
				// Reuse the earlier tombstone, which keeps probe runs short and
				// does not consume a fresh slot.
				e.idx[firstTomb] = fastSlot{hash: h, off: off, state: stFull}
			} else {
				*s = fastSlot{hash: h, off: off, state: stFull}
				e.used++
			}
			e.live++
			return
		case stTomb:
			if firstTomb < 0 {
				firstTomb = int(i)
			}
		case stFull:
			if s.hash == h {
				if k, _ := e.recordAt(s.off); string(k) == string(key) {
					s.off = e.appendRecord(key, val)
					return
				}
			}
		}
	}
}

func (e *faster) grow() {
	old := e.idx
	e.idx = make([]fastSlot, len(old)*2)
	e.used, e.live = 0, 0
	mask := uint64(len(e.idx) - 1)
	for _, s := range old {
		if s.state != stFull {
			continue
		}
		for i := s.hash & mask; ; i = (i + 1) & mask {
			if e.idx[i].state == stEmpty {
				e.idx[i] = s
				e.used++
				e.live++
				break
			}
		}
	}
}

func (e *faster) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.idx) == 0 {
		return nil, false, nil
	}
	h := hash64(key)
	mask := uint64(len(e.idx) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		s := e.idx[i]
		switch s.state {
		case stEmpty:
			return nil, false, nil
		case stFull:
			if s.hash == h {
				if k, v := e.recordAt(s.off); string(k) == string(key) {
					return v, true, nil
				}
			}
		}
	}
}

func (e *faster) Put(_ context.Context, key, val []byte) error {
	e.mu.Lock()
	e.putLocked(key, val)
	e.mu.Unlock()
	return nil
}

func (e *faster) deleteLocked(key []byte) {
	if len(e.idx) == 0 {
		return
	}
	h := hash64(key)
	mask := uint64(len(e.idx) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		s := &e.idx[i]
		if s.state == stEmpty {
			return
		}
		if s.state == stFull && s.hash == h {
			if k, _ := e.recordAt(s.off); string(k) == string(key) {
				s.state = stTomb
				e.live--
				return
			}
		}
	}
}

func (e *faster) Delete(_ context.Context, key []byte) error {
	e.mu.Lock()
	e.deleteLocked(key)
	e.mu.Unlock()
	return nil
}

func (e *faster) NewBatch() engine.Batch                                { return &memBatch{apply: e.applyBatch} }
func (e *faster) Scan(context.Context, []byte) (engine.Iterator, error) { return emptyIter{}, nil }
func (e *faster) Flush(context.Context) error                           { return nil }
func (e *faster) Stats(context.Context) (engine.Stats, error)           { return engine.UnknownStats(), nil }
func (e *faster) Close(context.Context) error                           { return nil }

func (e *faster) applyBatch(ops []memOp) {
	e.mu.Lock()
	for _, o := range ops {
		if o.del {
			e.deleteLocked(o.k)
		} else {
			e.putLocked(o.k, o.v)
		}
	}
	e.mu.Unlock()
}

// ---- swiss: single open-addressing table ----

// swiss is one open-addressing table in the Swiss-table probe shape: a key's
// hash picks a start slot and a linear probe walks forward to the key or the
// first empty slot. Keys and values are copied into the slot, so a Get returns a
// view into the slot's value. It is the bare point-lookup ceiling for a single
// flat table, guarded by one RWMutex. Like faster, a delete leaves a tombstone
// the probe walks past, so a removed key never truncates a later key's run.
type swiss struct {
	mu    sync.RWMutex
	slots []swissSlot
	live  int // full slots
	used  int // full + tombstone slots, the load-factor driver
}

type swissSlot struct {
	hash  uint64
	key   []byte
	val   []byte
	state slotState
}

const swissMaxLoad = 0.75

func newSwiss() *swiss { return &swiss{slots: make([]swissSlot, 1<<10)} }

func (e *swiss) Meta() engine.Meta {
	return engine.Meta{
		Name: "swiss", Family: engine.FamilyInMemory, Mode: engine.ModeInProc,
		Version: "builtin",
		Caps:    engine.Capabilities{AtomicBatch: true, PureNoCgo: true},
		Asterisks: []engine.Asterisk{
			{Code: "non-durable", Note: "in-memory Swiss-table-shaped ceiling: one open-addressing table, linear probe, keys and values copied into slots, no persistence. Unordered, point cells only. Reads return a view into the slot value; conc=1 is the single-thread table cost, conc=N the single guard's scaling."},
		},
	}
}

func (e *swiss) Open(context.Context, engine.Config) error { return nil }

func (e *swiss) putLocked(key, val []byte) {
	if float64(e.used+1) > swissMaxLoad*float64(len(e.slots)) {
		e.grow()
	}
	h := hash64(key)
	mask := uint64(len(e.slots) - 1)
	firstTomb := -1
	for i := h & mask; ; i = (i + 1) & mask {
		s := &e.slots[i]
		switch s.state {
		case stEmpty:
			ns := swissSlot{hash: h, key: append([]byte(nil), key...), val: append([]byte(nil), val...), state: stFull}
			if firstTomb >= 0 {
				e.slots[firstTomb] = ns
			} else {
				*s = ns
				e.used++
			}
			e.live++
			return
		case stTomb:
			if firstTomb < 0 {
				firstTomb = int(i)
			}
		case stFull:
			if s.hash == h && string(s.key) == string(key) {
				s.val = append(s.val[:0], val...)
				return
			}
		}
	}
}

func (e *swiss) grow() {
	old := e.slots
	e.slots = make([]swissSlot, len(old)*2)
	e.used, e.live = 0, 0
	mask := uint64(len(e.slots) - 1)
	for _, s := range old {
		if s.state != stFull {
			continue
		}
		for i := s.hash & mask; ; i = (i + 1) & mask {
			if e.slots[i].state == stEmpty {
				e.slots[i] = s
				e.used++
				e.live++
				break
			}
		}
	}
}

func (e *swiss) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	h := hash64(key)
	mask := uint64(len(e.slots) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		s := e.slots[i]
		switch s.state {
		case stEmpty:
			return nil, false, nil
		case stFull:
			if s.hash == h && string(s.key) == string(key) {
				return s.val, true, nil
			}
		}
	}
}

func (e *swiss) Put(_ context.Context, key, val []byte) error {
	e.mu.Lock()
	e.putLocked(key, val)
	e.mu.Unlock()
	return nil
}

func (e *swiss) deleteLocked(key []byte) {
	h := hash64(key)
	mask := uint64(len(e.slots) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		s := &e.slots[i]
		if s.state == stEmpty {
			return
		}
		if s.state == stFull && s.hash == h && string(s.key) == string(key) {
			s.state, s.key, s.val = stTomb, nil, nil
			e.live--
			return
		}
	}
}

func (e *swiss) Delete(_ context.Context, key []byte) error {
	e.mu.Lock()
	e.deleteLocked(key)
	e.mu.Unlock()
	return nil
}

func (e *swiss) NewBatch() engine.Batch                                { return &memBatch{apply: e.applyBatch} }
func (e *swiss) Scan(context.Context, []byte) (engine.Iterator, error) { return emptyIter{}, nil }
func (e *swiss) Flush(context.Context) error                           { return nil }
func (e *swiss) Stats(context.Context) (engine.Stats, error)           { return engine.UnknownStats(), nil }
func (e *swiss) Close(context.Context) error                           { return nil }

func (e *swiss) applyBatch(ops []memOp) {
	e.mu.Lock()
	for _, o := range ops {
		if o.del {
			e.deleteLocked(o.k)
		} else {
			e.putLocked(o.k, o.v)
		}
	}
	e.mu.Unlock()
}

// ---- otter: sharded map ----

// otter sketches otter's per-shard layout: the keyspace is split across a fixed
// power-of-two set of shards, each a plain Go map under its own RWMutex, so
// concurrent clients on different shards never contend. It is the ceiling that
// answers "what does sharding alone buy a map", the shape a concurrent in-memory
// cache reaches for before any eviction policy.
type otter struct {
	shards []otterShard
	mask   uint64
}

type otterShard struct {
	mu sync.RWMutex
	m  map[string][]byte
	_  [40]byte // pad the shard past a cache line so neighbors do not false-share
}

func newOtter() *otter {
	const n = 64
	o := &otter{shards: make([]otterShard, n), mask: n - 1}
	for i := range o.shards {
		o.shards[i].m = make(map[string][]byte)
	}
	return o
}

func (e *otter) Meta() engine.Meta {
	return engine.Meta{
		Name: "otter", Family: engine.FamilyInMemory, Mode: engine.ModeInProc,
		Version: "builtin",
		Caps:    engine.Capabilities{AtomicBatch: true, PureNoCgo: true},
		Asterisks: []engine.Asterisk{
			{Code: "non-durable", Note: "in-memory otter-shaped ceiling: 64 map shards, each under its own RWMutex, no persistence and no eviction. Unordered, point cells only. It isolates what sharding alone buys a map under concurrent clients, so the conc=N cell is the interesting one here."},
		},
	}
}

func (e *otter) Open(context.Context, engine.Config) error { return nil }

func (e *otter) shardFor(key []byte) *otterShard { return &e.shards[hash64(key)&e.mask] }

func (e *otter) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	s := e.shardFor(key)
	s.mu.RLock()
	v, ok := s.m[string(key)]
	s.mu.RUnlock()
	return v, ok, nil
}

func (e *otter) Put(_ context.Context, key, val []byte) error {
	s := e.shardFor(key)
	s.mu.Lock()
	s.m[string(key)] = append([]byte(nil), val...)
	s.mu.Unlock()
	return nil
}

func (e *otter) Delete(_ context.Context, key []byte) error {
	s := e.shardFor(key)
	s.mu.Lock()
	delete(s.m, string(key))
	s.mu.Unlock()
	return nil
}

func (e *otter) NewBatch() engine.Batch                                { return &memBatch{apply: e.applyBatch} }
func (e *otter) Scan(context.Context, []byte) (engine.Iterator, error) { return emptyIter{}, nil }
func (e *otter) Flush(context.Context) error                           { return nil }
func (e *otter) Stats(context.Context) (engine.Stats, error)           { return engine.UnknownStats(), nil }
func (e *otter) Close(context.Context) error                           { return nil }

func (e *otter) applyBatch(ops []memOp) {
	for _, o := range ops {
		if o.del {
			s := e.shardFor(o.k)
			s.mu.Lock()
			delete(s.m, string(o.k))
			s.mu.Unlock()
		} else {
			s := e.shardFor(o.k)
			s.mu.Lock()
			s.m[string(o.k)] = o.v
			s.mu.Unlock()
		}
	}
}

// ---- shared batch ----

// memBatch buffers writes and replays them through the engine's apply on Commit.
// Each engine passes its own apply so the batch stays engine-agnostic.
type memBatch struct {
	ops   []memOp
	apply func([]memOp)
}

type memOp struct {
	del  bool
	k, v []byte
}

func (b *memBatch) Put(k, v []byte) {
	b.ops = append(b.ops, memOp{k: append([]byte(nil), k...), v: append([]byte(nil), v...)})
}
func (b *memBatch) Delete(k []byte) {
	b.ops = append(b.ops, memOp{del: true, k: append([]byte(nil), k...)})
}
func (b *memBatch) Len() int { return len(b.ops) }
func (b *memBatch) Commit(_ context.Context) error {
	b.apply(b.ops)
	b.ops = nil
	return nil
}
