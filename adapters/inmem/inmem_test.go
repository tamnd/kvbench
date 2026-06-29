package inmem

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/tamnd/kvbench/engine"
)

// ceilingEngines is every in-memory ceiling that actually stores data, so the
// oracle test below can run the same script against each. devnull is excluded
// because it stores nothing by design and is checked on its own.
var ceilingEngines = []string{"faster", "f2", "otter", "swiss"}

func key(i int) []byte { return []byte(fmt.Sprintf("key:%08d", i)) }
func val(i int) []byte { return []byte(fmt.Sprintf("val-%08d-payload", i)) }

// TestCeilingOracle checks each storing ceiling against a Go map: every key put
// reads back its newest value, an overwrite wins, a delete removes the key, and
// a missing key reports not-found. This is the correctness floor under the
// throughput numbers; a fast structure that loses writes is worthless, and the
// hash-distribution bug that first shipped here (a multiply by zero that
// annihilated the first key word and clustered the probe chains) would have
// passed a throughput run while silently degrading, so the oracle exercises
// enough distinct keys to walk past the initial table growth.
func TestCeilingOracle(t *testing.T) {
	ctx := context.Background()
	const n = 5000
	for _, name := range ceilingEngines {
		t.Run(name, func(t *testing.T) {
			e, err := engine.New(name)
			if err != nil {
				t.Fatalf("new %s: %v", name, err)
			}
			if err := e.Open(ctx, engine.Config{}); err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = e.Close(ctx) }()

			oracle := map[string][]byte{}

			put := func(k, v []byte) {
				if err := e.Put(ctx, k, v); err != nil {
					t.Fatalf("put: %v", err)
				}
				oracle[string(k)] = v
			}

			// First write of every key.
			for i := 0; i < n; i++ {
				put(key(i), val(i))
			}
			// Overwrite the even keys so the newest value must win.
			for i := 0; i < n; i += 2 {
				put(key(i), val(i+1_000_000))
			}
			// Delete every fifth key.
			for i := 0; i < n; i += 5 {
				if err := e.Delete(ctx, key(i)); err != nil {
					t.Fatalf("delete: %v", err)
				}
				delete(oracle, string(key(i)))
			}

			// Every surviving key reads back its oracle value.
			for i := 0; i < n; i++ {
				got, found, err := e.Get(ctx, key(i))
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				want, ok := oracle[string(key(i))]
				if ok != found {
					t.Fatalf("key %d: found=%v want %v", i, found, ok)
				}
				if found && string(got) != string(want) {
					t.Fatalf("key %d: got %q want %q", i, got, want)
				}
			}
			// A never-inserted key is absent.
			if _, found, _ := e.Get(ctx, []byte("absent")); found {
				t.Fatalf("absent key reported present")
			}
		})
	}
}

// TestCeilingBatch checks the shared batch path the loader drives: a batch of
// puts and deletes applies atomically enough that every key reads back its
// post-commit value.
func TestCeilingBatch(t *testing.T) {
	ctx := context.Background()
	const n = 2000
	for _, name := range ceilingEngines {
		t.Run(name, func(t *testing.T) {
			e, _ := engine.New(name)
			if err := e.Open(ctx, engine.Config{}); err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = e.Close(ctx) }()

			b := e.NewBatch()
			for i := 0; i < n; i++ {
				b.Put(key(i), val(i))
			}
			if b.Len() != n {
				t.Fatalf("batch len %d want %d", b.Len(), n)
			}
			if err := b.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}
			for i := 0; i < n; i++ {
				got, found, _ := e.Get(ctx, key(i))
				if !found || string(got) != string(val(i)) {
					t.Fatalf("key %d: got %q found %v", i, got, found)
				}
			}
		})
	}
}

// TestDevnullFloor pins devnull's contract: it stores nothing, so every read
// misses, and every write and batch is accepted without error. Its value is the
// dispatch-floor measurement, not storage, so the only thing to verify is that
// it never errors and never returns data.
func TestDevnullFloor(t *testing.T) {
	ctx := context.Background()
	e, _ := engine.New("devnull")
	if err := e.Open(ctx, engine.Config{}); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = e.Close(ctx) }()

	if err := e.Put(ctx, key(1), val(1)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, found, err := e.Get(ctx, key(1)); err != nil || found {
		t.Fatalf("devnull get: found=%v err=%v, want absent", found, err)
	}
	b := e.NewBatch()
	b.Put(key(2), val(2))
	if b.Len() != 1 {
		t.Fatalf("batch len %d want 1", b.Len())
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestF2Concurrent drives f2 the way the harness does at conc=N: many writers
// filling and overwriting shards while many readers probe the same keyspace,
// with -race on. It guards the lock-free read path, the read-copy-update on
// overwrite, and the table swap a grow performs, none of which the single-thread
// oracle above can stress. It asserts liveness rather than a point-in-time
// value, because a key a reader sees may be one a writer is still rewriting; the
// race detector catches the bug this test exists to catch.
func TestF2Concurrent(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New("f2")
	if err != nil {
		t.Fatalf("new f2: %v", err)
	}
	if err := e.Open(ctx, engine.Config{}); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = e.Close(ctx) }()

	const (
		writers = 4
		readers = 4
		keys    = 20000
		rounds  = 3
	)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(shard int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				for i := shard; i < keys; i += writers {
					if err := e.Put(ctx, key(i), val(i+r*1_000_000)); err != nil {
						t.Errorf("put: %v", err)
						return
					}
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < keys*rounds; n++ {
				_, _, err := e.Get(ctx, key(n%keys))
				if err != nil {
					t.Errorf("get: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// After every writer's last round, every key reads back present.
	for i := 0; i < keys; i++ {
		if _, found, _ := e.Get(ctx, key(i)); !found {
			t.Fatalf("key %d missing after concurrent fill", i)
		}
	}
}

// TestHashDistribution is a direct guard on the probe-table health: hashing a
// run of realistic keys must not pile them into a handful of low-bit buckets,
// the failure that turned the open-addressing tables into linear scans. It
// checks that the low bits used to index a table spread close to uniform.
func TestHashDistribution(t *testing.T) {
	const (
		keys    = 1 << 16
		buckets = 1 << 8 // the low byte, the first table's index width
	)
	counts := make([]int, buckets)
	for i := 0; i < keys; i++ {
		counts[hash64(key(i))&(buckets-1)]++
	}
	mean := float64(keys) / float64(buckets)
	for b, c := range counts {
		// A healthy hash keeps every bucket within a wide band of the mean; a
		// clustering hash leaves most buckets near empty and a few overloaded.
		if float64(c) < mean*0.5 || float64(c) > mean*1.5 {
			t.Fatalf("bucket %d holds %d keys, mean %.0f: hash low bits cluster", b, c, mean)
		}
	}
}
