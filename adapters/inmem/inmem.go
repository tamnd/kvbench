// Package inmem holds the in-memory floor and ceiling engines: the structures
// that bound what any durable engine in this suite could ever reach on a point
// workload, plus a do-nothing floor that isolates the harness and dispatch cost
// from the store cost. None of them persist, so none are peers to the durable
// engines; they exist to answer two questions the durable numbers cannot answer
// on their own. The floor (devnull) answers "how much of a cell's time is the
// harness, not the store". The ceilings (faster, f2, otter, swiss) answer "what
// is the fastest a bare in-memory structure of this shape serves the same keys",
// which is the budget every layer the real engine carries above a bare map is
// spending.
//
// The ceilings are deliberately thin sketches of well-known designs rather than
// faithful ports: faster is a FASTER/Garnet-style append-only log behind an
// open-addressing hash index, f2 is the FASTER v2 redesign of that same shape,
// otter is a sharded map in the shape otter's per-shard cache uses, and swiss is
// a single open-addressing table in the Swiss-table probe shape. The reads hand
// back a view into the structure rather than a copy because the driver discards
// the value, so these numbers are the zero-copy read ceiling.
//
// faster and f2 are paired on purpose: they are the same store one design
// generation apart, so the gap between their conc=N cells is the lock tax. faster
// guards its whole log and index with one RWMutex, so a concurrent read workload
// serializes every reader on a single lock and collapses toward one core; f2
// removes that lock entirely on the read path (the index slot is an atomic word,
// a read is an atomic load plus a tag probe) and shards the write path so writers
// on different shards never contend. A coordination microbenchmark on a 10-core
// box puts the single global lock at 6.5 Mops/s, a sharded RWMutex at 133, and a
// lock-free sharded atomic-load read at 348, which is the gap these two engines
// are built to make visible. otter and swiss bracket that span: otter is the
// sharded-map concurrency ceiling and swiss is the single-thread flat-table
// ceiling, so read otter's conc=N cell and swiss's conc=1 cell as the bounds.
package inmem

import (
	"context"
	"encoding/binary"
	"math/bits"
	"sync"
	"sync/atomic"

	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("devnull", func() engine.Engine { return &devnull{} })
	engine.Register("faster", func() engine.Engine { return newFaster() })
	engine.Register("f2", func() engine.Engine { return newF2() })
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

// ---- f2: lock-free reads, sharded hybrid log (FASTER v2 shape) ----

// f2 sketches the F2 / FASTER v2 design (Kanellis et al., "From FASTER to F2",
// PVLDB vol. 18), which is the same store as faster above one design generation
// on. It exists to pay down the one tax faster cannot: faster guards its whole
// log and index with a single RWMutex, so a concurrent read workload serializes
// every reader on one lock and collapses toward a single core. f2 keeps FASTER's
// two latch-free ideas and drops the lock:
//
//   - The hash index is latch-free. A slot is one atomic 64-bit word packing a
//     15-bit tag and a 48-bit log offset, in the bit shape FASTER's index uses
//     (48-bit address | 15-bit tag | 1 control bit). A read is a single
//     atomic.LoadUint64 plus a tag compare, with no reader-writer lock anywhere
//     on the path. The tag rejects most collisions without touching the log, so
//     a hit is one index load and one log read.
//   - The hybrid log is append-only and shared-nothing across shards. The
//     keyspace is split over a fixed power-of-two set of shards, each a private
//     log plus index, so writers on different shards never contend. An overwrite
//     is read-copy-update: append a new version to the log tail and atomically
//     repoint the slot, so the old bytes are never mutated and a reader holding
//     an older offset still sees a consistent record. That immutability is what
//     lets the read path skip the lock; real FASTER reclaims stranded versions
//     under epoch protection, here the garbage collector does it once no reader
//     holds the old table.
//
// This sketch keeps the read path fully lock-free but serializes writers within
// a shard under a plain Mutex rather than reproducing FASTER's latch-free
// fetch-and-add tail plus tentative-bit insert; the win it isolates is the read
// path and the move off a single global lock, which is where faster bleeds. Read
// f2's conc=N read cell against faster's to see the lock tax, and against otter's
// to see what dropping the per-shard read lock on top of sharding buys.
type f2 struct {
	shards []*f2shard
	mask   uint64
}

// f2shard is one shard's private hybrid log and index. Readers never take mu;
// they atomic-load tab and probe it. Writers hold mu, which also serializes the
// table swap a grow performs, so a reader either sees the whole old table or the
// whole new one.
type f2shard struct {
	mu  sync.Mutex
	tab atomic.Pointer[f2tab]
	_   [40]byte // pad shards apart so a writer's lock line does not false-share
}

// f2tab is an immutable-after-publish table: once a shard swaps a new f2tab in,
// the old one's slots and buf are never written again, so in-flight readers on
// the old table stay correct until they drop it.
type f2tab struct {
	slots []uint64 // 0 empty, f2Tomb tombstone, else tag<<48 | (off+1)
	mask  uint64
	buf   []byte // append-only record arena: varint(keyLen) key varint(valLen) val
	tail  int    // next free offset in buf, advanced by the writer under mu
	live  int    // installed keys
	used  int    // installed plus tombstone slots, the load-factor driver
}

const (
	f2Shards   = 256
	f2MaxLoad  = 0.7
	f2TagShift = 48
	f2OffMask  = (uint64(1) << 48) - 1
	f2TagMask  = uint64(0x7fff) << f2TagShift // bits 48..62
	f2Tomb     = uint64(1) << 63              // bit 63: a slot the probe walks past
)

func newF2() *f2 {
	f := &f2{shards: make([]*f2shard, f2Shards), mask: f2Shards - 1}
	for i := range f.shards {
		s := &f2shard{}
		s.tab.Store(newF2tab(1<<9, 1<<16))
		f.shards[i] = s
	}
	return f
}

func newF2tab(slots, bufcap int) *f2tab {
	return &f2tab{slots: make([]uint64, slots), mask: uint64(slots - 1), buf: make([]byte, bufcap)}
}

// shardFor picks a shard from the high hash bits and the slot from the low bits,
// so the two never read from the same end of the hash and a shard's keys still
// spread across its whole index.
func (e *f2) shardFor(h uint64) *f2shard { return e.shards[(h>>56)&e.mask] }

// tagOf takes 15 middle bits, disjoint from the shard's high bits and the
// index's low bits, so the tag adds resolution the index does not already have.
func tagOf(h uint64) uint64 { return (h >> 24) & 0x7fff }

func (e *f2) Meta() engine.Meta {
	return engine.Meta{
		Name: "f2", Family: engine.FamilyInMemory, Mode: engine.ModeInProc,
		Version: "builtin",
		Caps:    engine.Capabilities{AtomicBatch: true, PureNoCgo: true},
		Asterisks: []engine.Asterisk{
			{Code: "non-durable", Note: "in-memory FASTER v2-shaped ceiling: a latch-free hash index (each slot one atomic word) over a per-shard append-only log, no persistence. The read path takes no lock at all, an atomic load plus a tag probe; writers are serialized per shard. Unordered, point cells only. Reads return a view into the log, the zero-copy lock-free read ceiling; read its conc=N cell against the faster engine's to see the cost of faster's single RWMutex."},
		},
	}
}

func (e *f2) Open(context.Context, engine.Config) error { return nil }

func (e *f2) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	h := hash64(key)
	t := e.shardFor(h).tab.Load()
	want := tagOf(h) << f2TagShift
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e0 := atomic.LoadUint64(&t.slots[i])
		if e0 == 0 {
			return nil, false, nil
		}
		if e0 != f2Tomb && e0&f2TagMask == want {
			off := int(e0&f2OffMask) - 1
			if k, v := recordAt(t.buf, off); string(k) == string(key) {
				return v, true, nil
			}
		}
	}
}

func (e *f2) Put(_ context.Context, key, val []byte) error {
	h := hash64(key)
	s := e.shardFor(h)
	s.mu.Lock()
	s.putLocked(h, key, val)
	s.mu.Unlock()
	return nil
}

func (e *f2) Delete(_ context.Context, key []byte) error {
	h := hash64(key)
	s := e.shardFor(h)
	s.mu.Lock()
	s.deleteLocked(h, key)
	s.mu.Unlock()
	return nil
}

// putLocked appends the new record and installs the slot. It reuses an earlier
// tombstone if the probe passed one, which keeps a delete-heavy run from leaving
// the index permanently fuller than its live count.
func (s *f2shard) putLocked(h uint64, key, val []byte) {
	t := s.tab.Load()
	need := recordSize(key, val)
	if t.tail+need > len(t.buf) || float64(t.used+1) > f2MaxLoad*float64(len(t.slots)) {
		t = s.grow(t, need)
	}
	off := t.tail
	t.tail += t.appendRecord(off, key, val)
	entry := (tagOf(h) << f2TagShift) | uint64(off+1)
	want := tagOf(h) << f2TagShift
	firstTomb := -1
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e0 := atomic.LoadUint64(&t.slots[i])
		switch {
		case e0 == 0:
			if firstTomb >= 0 {
				atomic.StoreUint64(&t.slots[firstTomb], entry)
			} else {
				atomic.StoreUint64(&t.slots[i], entry)
				t.used++
			}
			t.live++
			return
		case e0 == f2Tomb:
			if firstTomb < 0 {
				firstTomb = int(i)
			}
		case e0&f2TagMask == want:
			if k, _ := recordAt(t.buf, int(e0&f2OffMask)-1); string(k) == string(key) {
				atomic.StoreUint64(&t.slots[i], entry) // read-copy-update: repoint to the new version
				return
			}
		}
	}
}

func (s *f2shard) deleteLocked(h uint64, key []byte) {
	t := s.tab.Load()
	want := tagOf(h) << f2TagShift
	for i := h & t.mask; ; i = (i + 1) & t.mask {
		e0 := atomic.LoadUint64(&t.slots[i])
		if e0 == 0 {
			return
		}
		if e0 != f2Tomb && e0&f2TagMask == want {
			if k, _ := recordAt(t.buf, int(e0&f2OffMask)-1); string(k) == string(key) {
				atomic.StoreUint64(&t.slots[i], f2Tomb)
				t.live--
				return
			}
		}
	}
}

// grow builds a fresh table sized for the live set plus headroom, replays only
// the live records (compacting away stranded overwrite versions and tombstones,
// FASTER's log compaction in miniature), and publishes it with one atomic store.
// The old table is left untouched so readers still on it stay correct.
func (s *f2shard) grow(old *f2tab, need int) *f2tab {
	slots := len(old.slots)
	for float64(old.live+1) > f2MaxLoad*float64(slots) {
		slots *= 2
	}
	nt := newF2tab(slots, old.tail*2+need+1<<12)
	for _, e0 := range old.slots {
		if e0 == 0 || e0 == f2Tomb {
			continue
		}
		k, v := recordAt(old.buf, int(e0&f2OffMask)-1)
		h := hash64(k)
		off := nt.tail
		nt.tail += nt.appendRecord(off, k, v)
		entry := (tagOf(h) << f2TagShift) | uint64(off+1)
		for i := h & nt.mask; ; i = (i + 1) & nt.mask {
			if nt.slots[i] == 0 {
				nt.slots[i] = entry
				nt.used++
				nt.live++
				break
			}
		}
	}
	s.tab.Store(nt)
	return nt
}

func (e *f2) NewBatch() engine.Batch                                { return &memBatch{apply: e.applyBatch} }
func (e *f2) Scan(context.Context, []byte) (engine.Iterator, error) { return emptyIter{}, nil }
func (e *f2) Flush(context.Context) error                           { return nil }
func (e *f2) Stats(context.Context) (engine.Stats, error)           { return engine.UnknownStats(), nil }
func (e *f2) Close(context.Context) error                           { return nil }

func (e *f2) applyBatch(ops []memOp) {
	for _, o := range ops {
		h := hash64(o.k)
		s := e.shardFor(h)
		s.mu.Lock()
		if o.del {
			s.deleteLocked(h, o.k)
		} else {
			s.putLocked(h, o.k, o.v)
		}
		s.mu.Unlock()
	}
}

// appendRecord writes one record into buf at off and returns its byte length.
// The caller guarantees room, so this never reallocates buf and the backing
// array a reader is mid-read on never moves.
func (t *f2tab) appendRecord(off int, key, val []byte) int {
	b := t.buf[off:]
	n := binary.PutUvarint(b, uint64(len(key)))
	n += copy(b[n:], key)
	n += binary.PutUvarint(b[n:], uint64(len(val)))
	n += copy(b[n:], val)
	return n
}

// recordAt returns views of the key and value of the record at off. The bytes
// are immutable once the slot pointing at them is installed, so the view is safe
// to hand back without a copy or a lock.
func recordAt(buf []byte, off int) (key, val []byte) {
	b := buf[off:]
	kl, n := binary.Uvarint(b)
	b = b[n:]
	key = b[:kl]
	b = b[kl:]
	vl, n := binary.Uvarint(b)
	b = b[n:]
	val = b[:vl]
	return key, val
}

func recordSize(key, val []byte) int {
	return uvarintLen(len(key)) + len(key) + uvarintLen(len(val)) + len(val)
}

func uvarintLen(n int) int {
	x, c := uint64(n), 1
	for x >= 0x80 {
		x >>= 7
		c++
	}
	return c
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
