// Command locktax measures the coordination tax that separates the faster and
// f2 in-memory ceiling engines (adapters/inmem). It isolates the guard, not the
// store: every probe is a uint64-keyed open-addressed lookup, so the only thing
// that changes between rows is how a concurrent access is coordinated.
//
// It answers two questions the engine numbers raise. First, what does a single
// global lock cost under concurrency, the lock faster pays on every read.
// Second, how much of that does sharding recover, and how much more does a
// latch-free atomic-load read recover on top of sharding, which is what f2 does.
// The "shared-nothing" row is the no-coordination ceiling, the speed the same
// probe reaches when each core owns its own table and never touches another's.
//
// This is the grounding for the inmem package comment: on a 10-core box a single
// global Mutex serves a few Mops/s, a 256-shard RWMutex an order of magnitude
// more, and a 256-shard lock-free atomic load a few times more again. Run it on
// a box to see that box's numbers:
//
//	go run ./cmd/locktax
//	go run ./cmd/locktax -iters 50000000
package main

import (
	"flag"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	totalKeys = 1 << 20 // 1M live keys total across the keyspace
	shards    = 256     // a few times the core count, the shape the store uses
)

// tab is one open-addressed table: slot[i] holds a key (0 = empty). We never
// insert key 0. Value lookup is omitted; we measure the index-probe coordination.
type tab struct {
	slot []uint64
	mask uint64
}

func newTab(n uint64) *tab {
	sz := uint64(1024)
	for sz < n*2 { // load factor 0.5
		sz <<= 1
	}
	return &tab{slot: make([]uint64, sz), mask: sz - 1}
}

func mix(x uint64) uint64 { // splitmix64 finalizer
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func (t *tab) put(key uint64) {
	i := mix(key) & t.mask
	for t.slot[i] != 0 && t.slot[i] != key {
		i = (i + 1) & t.mask
	}
	t.slot[i] = key
}

func (t *tab) get(key uint64) bool {
	i := mix(key) & t.mask
	for {
		k := t.slot[i]
		if k == 0 {
			return false
		}
		if k == key {
			return true
		}
		i = (i + 1) & t.mask
	}
}

func (t *tab) getAtomic(key uint64) bool {
	i := mix(key) & t.mask
	for {
		k := atomic.LoadUint64(&t.slot[i])
		if k == 0 {
			return false
		}
		if k == key {
			return true
		}
		i = (i + 1) & t.mask
	}
}

func xorshift(x *uint32) uint32 {
	*x ^= *x << 13
	*x ^= *x >> 17
	*x ^= *x << 5
	return *x
}

var iters = 30_000_000

// ---- serial single-core ns/op for each guard ----

func serial(name string, body func(key uint64)) {
	var x uint32 = 2463534242
	start := time.Now()
	for n := 0; n < iters; n++ {
		body(uint64(xorshift(&x)%totalKeys) + 1)
	}
	d := time.Since(start)
	mops := float64(iters) / d.Seconds() / 1e6
	fmt.Printf("  %-28s %6.1f Mops/s  (%.2f ns/op)\n", name, mops, 1000/mops)
}

// ---- parallel sharded guards ----

type lockedShard struct {
	rw sync.RWMutex
	mu sync.Mutex
	t  *tab
	_  [16]byte // separate hot fields onto their own line-ish
}

func buildShards() ([]*lockedShard, uint64) {
	sh := make([]*lockedShard, shards)
	for i := range sh {
		sh[i] = &lockedShard{t: newTab(totalKeys/shards + 16)}
	}
	for k := uint64(1); k <= totalKeys; k++ {
		sh[(mix(k)>>40)&(shards-1)].t.put(k)
	}
	return sh, shards - 1
}

func parallelSharded(name string, g int, op func(s *lockedShard, key uint64)) {
	sh, smask := buildShards()
	per := iters
	var wg sync.WaitGroup
	start := time.Now()
	for gi := 0; gi < g; gi++ {
		wg.Add(1)
		go func(seed uint32) {
			defer wg.Done()
			x := seed
			for n := 0; n < per; n++ {
				key := uint64(xorshift(&x)%totalKeys) + 1
				op(sh[(mix(key)>>40)&smask], key)
			}
		}(uint32(2463534242 + gi*2654435761))
	}
	wg.Wait()
	d := time.Since(start)
	mops := float64(g*per) / d.Seconds() / 1e6
	fmt.Printf("  %-34s %8.1f Mops/s\n", name, mops)
}

// shared-nothing: G goroutines, each owns one private table, no guard at all.
func parallelSharedNothing(g int) float64 {
	tabs := make([]*tab, g)
	for i := range tabs {
		tabs[i] = newTab(totalKeys/uint64(g) + 16)
		for k := uint64(1); k <= totalKeys/uint64(g); k++ {
			tabs[i].put(uint64(i)*totalKeys + k)
		}
	}
	per := iters
	nlocal := totalKeys / uint64(g)
	var wg sync.WaitGroup
	start := time.Now()
	for gi := 0; gi < g; gi++ {
		wg.Add(1)
		go func(t *tab, base uint64, seed uint32) {
			defer wg.Done()
			x := seed
			for n := 0; n < per; n++ {
				key := base + uint64(xorshift(&x)%uint32(nlocal)) + 1
				t.get(key)
			}
		}(tabs[gi], uint64(gi)*totalKeys, uint32(2463534242+gi*2654435761))
	}
	wg.Wait()
	d := time.Since(start)
	return float64(g*per) / d.Seconds() / 1e6
}

func main() {
	flag.IntVar(&iters, "iters", iters, "iterations per goroutine")
	flag.Parse()
	g := runtime.GOMAXPROCS(0)
	fmt.Printf("GOMAXPROCS=%d  keys=%d  shards=%d  iters/goroutine=%d\n\n", g, totalKeys, shards, iters)

	one := newTab(totalKeys)
	for k := uint64(1); k <= totalKeys; k++ {
		one.put(k)
	}
	var rw sync.RWMutex
	var mu sync.Mutex

	fmt.Println("serial, single core (per-op guard cost):")
	serial("plain (no guard)", func(k uint64) { one.get(k) })
	serial("atomic.LoadUint64 read", func(k uint64) { one.getAtomic(k) })
	serial("Mutex.Lock/Unlock", func(k uint64) { mu.Lock(); one.get(k); mu.Unlock() })
	serial("RWMutex.RLock/RUnlock", func(k uint64) { rw.RLock(); one.get(k); rw.RUnlock() })

	fmt.Printf("\nparallel, %d cores (aggregate throughput):\n", g)
	parallelSharded("256-shard RWMutex (faster read path)", g, func(s *lockedShard, k uint64) {
		s.rw.RLock()
		s.t.get(k)
		s.rw.RUnlock()
	})
	parallelSharded("256-shard plain Mutex (f2 write path)", g, func(s *lockedShard, k uint64) {
		s.mu.Lock()
		s.t.get(k)
		s.mu.Unlock()
	})
	parallelSharded("256-shard atomic load (f2 read path)", g, func(s *lockedShard, k uint64) {
		s.t.getAtomic(k)
	})
	parallelSharded("single global Mutex (baseline)", g, func(s *lockedShard, k uint64) {
		mu.Lock()
		s.t.get(k)
		mu.Unlock()
	})
	sn := parallelSharedNothing(g)
	fmt.Printf("  %-34s %8.1f Mops/s\n", "shared-nothing, no guard (ceiling)", sn)
}
