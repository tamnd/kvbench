// Package workload defines the benchmark workloads (YCSB A-F and db_bench-style)
// and the deterministic, seeded operation generator that feeds every engine
// identically. No wall-clock randomness: the same seed reproduces the exact
// stream so two engines see byte-identical keys, values, and op order.
package workload

import (
	"encoding/binary"
	"math"
)

// OpKind is one operation in a workload stream.
type OpKind uint8

const (
	OpRead OpKind = iota
	OpUpdate
	OpInsert
	OpScan
	OpRMW // read-modify-write
	OpDelete
)

// Op is a single generated operation.
type Op struct {
	Kind    OpKind
	Key     []byte
	Value   []byte // for writes
	ScanLen int    // for OpScan
}

// Dist is a key-selection distribution.
type Dist uint8

const (
	DistUniform Dist = iota
	DistZipfian
	DistLatest
)

// Spec describes a workload: its op mix, distribution, and whether it needs an
// ordered engine.
type Spec struct {
	Name       string
	ReadPct    int
	UpdatePct  int
	InsertPct  int
	ScanPct    int
	RMWPct     int
	DeletePct  int
	Dist       Dist
	NeedsScan  bool
	WriteOnly  bool // pure load workloads (fillseq/fillrandom/overwrite)
	Sequential bool // sequential keys (fillseq)
}

// Catalog is the full workload set.
var Catalog = map[string]Spec{
	// YCSB core
	"ycsb-a": {Name: "ycsb-a", ReadPct: 50, UpdatePct: 50, Dist: DistZipfian},
	"ycsb-b": {Name: "ycsb-b", ReadPct: 95, UpdatePct: 5, Dist: DistZipfian},
	"ycsb-c": {Name: "ycsb-c", ReadPct: 100, Dist: DistZipfian},
	"ycsb-d": {Name: "ycsb-d", ReadPct: 95, InsertPct: 5, Dist: DistLatest},
	"ycsb-e": {Name: "ycsb-e", ScanPct: 95, InsertPct: 5, Dist: DistZipfian, NeedsScan: true},
	"ycsb-f": {Name: "ycsb-f", ReadPct: 50, RMWPct: 50, Dist: DistZipfian},
	// db_bench-style
	"fillseq":      {Name: "fillseq", InsertPct: 100, WriteOnly: true, Sequential: true},
	"fillrandom":   {Name: "fillrandom", InsertPct: 100, WriteOnly: true, Dist: DistUniform},
	"overwrite":    {Name: "overwrite", UpdatePct: 100, WriteOnly: true, Dist: DistUniform},
	"readrandom":   {Name: "readrandom", ReadPct: 100, Dist: DistUniform},
	"readseq":      {Name: "readseq", ScanPct: 100, NeedsScan: true, Dist: DistUniform},
	"deleterandom": {Name: "deleterandom", DeletePct: 100, WriteOnly: true, Dist: DistUniform},
}

// Generator produces a deterministic op stream for one client goroutine.
// Each client gets its own Generator seeded distinctly so clients do not all
// hit the same keys, but the whole run is reproducible from the base seed.
type Generator struct {
	spec       Spec
	rng        *splitmix64
	zipf       *zipf
	keyspace   uint64
	valBytes   int
	keyBytes   int
	valTmpl    []byte
	insertHead uint64 // for latest/insert workloads
	seqCursor  uint64
}

// NewGenerator builds a generator. keyspace is the number of distinct keys in
// the loaded dataset; clientID distinguishes parallel clients.
func NewGenerator(spec Spec, seed uint64, clientID int, keyspace uint64, valBytes int) *Generator {
	g := &Generator{
		spec:     spec,
		rng:      newSplitmix64(seed ^ (uint64(clientID+1) * 0x9E3779B97F4A7C15)),
		keyspace: keyspace,
		valBytes: valBytes,
		keyBytes: 16,
	}
	if spec.Dist == DistZipfian {
		g.zipf = newZipf(keyspace, 0.99, g.rng)
	}
	// deterministic value template; writes copy & tag it so values vary.
	g.valTmpl = make([]byte, valBytes)
	for i := range g.valTmpl {
		g.valTmpl[i] = byte('a' + (i % 26))
	}
	g.insertHead = keyspace
	g.seqCursor = uint64(clientID) // fillseq partitions the keyspace by client
	return g
}

// Next returns the next operation. The returned Op's Key/Value slices are
// freshly allocated per call (callers may retain them within the call only).
func (g *Generator) Next() Op {
	kind := g.pick()
	switch kind {
	case OpScan:
		return Op{Kind: OpScan, Key: g.selectKey(), ScanLen: 1 + int(g.rng.next()%100)}
	case OpInsert:
		k := g.nextInsertKey()
		return Op{Kind: OpInsert, Key: k, Value: g.makeValue(k)}
	case OpUpdate:
		k := g.selectKey()
		return Op{Kind: OpUpdate, Key: k, Value: g.makeValue(k)}
	case OpRMW:
		k := g.selectKey()
		return Op{Kind: OpRMW, Key: k, Value: g.makeValue(k)}
	case OpDelete:
		return Op{Kind: OpDelete, Key: g.selectKey()}
	default:
		return Op{Kind: OpRead, Key: g.selectKey()}
	}
}

func (g *Generator) pick() OpKind {
	if g.spec.WriteOnly {
		switch {
		case g.spec.InsertPct == 100:
			return OpInsert
		case g.spec.UpdatePct == 100:
			return OpUpdate
		case g.spec.DeletePct == 100:
			return OpDelete
		}
	}
	r := int(g.rng.next() % 100)
	c := 0
	if c += g.spec.ReadPct; r < c {
		return OpRead
	}
	if c += g.spec.UpdatePct; r < c {
		return OpUpdate
	}
	if c += g.spec.InsertPct; r < c {
		return OpInsert
	}
	if c += g.spec.ScanPct; r < c {
		return OpScan
	}
	if c += g.spec.RMWPct; r < c {
		return OpRMW
	}
	if c += g.spec.DeletePct; r < c {
		return OpDelete
	}
	return OpRead
}

// selectKey chooses an existing key per the distribution.
func (g *Generator) selectKey() []byte {
	var idx uint64
	switch g.spec.Dist {
	case DistZipfian:
		idx = g.zipf.next()
	case DistLatest:
		// recency-skewed: favor recently inserted keys near insertHead.
		span := g.insertHead
		if span == 0 {
			span = 1
		}
		// zipf-like pull toward the head
		z := g.rng.next() % span
		idx = span - 1 - (z * z / span)
	default:
		idx = g.rng.next() % g.keyspace
	}
	return EncodeKey(idx)
}

func (g *Generator) nextInsertKey() []byte {
	if g.spec.Sequential {
		k := g.seqCursor
		g.seqCursor++
		return EncodeKey(k)
	}
	// random fill across the keyspace
	return EncodeKey(g.rng.next() % g.keyspace)
}

func (g *Generator) makeValue(key []byte) []byte {
	v := make([]byte, g.valBytes)
	copy(v, g.valTmpl)
	// tag first 8 bytes with a key-derived stamp so values differ and are
	// incompressible-ish without being pure zeros.
	if len(v) >= 8 {
		binary.LittleEndian.PutUint64(v, g.rng.next())
	}
	if len(v) >= 16 {
		copy(v[8:16], key)
	}
	return v
}

// EncodeKey turns an index into a fixed-width, lexicographically-ordered key.
func EncodeKey(idx uint64) []byte {
	k := make([]byte, 16)
	binary.BigEndian.PutUint64(k[0:8], idx)
	// pad remaining bytes deterministically
	for i := 8; i < 16; i++ {
		k[i] = '0'
	}
	return k
}

// ---- deterministic PRNG (splitmix64) ----

type splitmix64 struct{ s uint64 }

func newSplitmix64(seed uint64) *splitmix64 { return &splitmix64{s: seed} }

func (r *splitmix64) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *splitmix64) float64() float64 {
	return float64(r.next()>>11) / float64(uint64(1)<<53)
}

// ---- Zipfian generator (Gray et al. / YCSB ScrambledZipfian style, simplified) ----

type zipf struct {
	n     uint64
	theta float64
	alpha float64
	zeta2 float64
	zetan float64
	eta   float64
	rng   *splitmix64
}

func newZipf(n uint64, theta float64, rng *splitmix64) *zipf {
	z := &zipf{n: n, theta: theta, rng: rng}
	z.zeta2 = zetaStatic(2, theta)
	z.zetan = zetaStatic(n, theta)
	z.alpha = 1.0 / (1.0 - theta)
	z.eta = (1 - math.Pow(2.0/float64(n), 1-theta)) / (1 - z.zeta2/z.zetan)
	return z
}

func zetaStatic(n uint64, theta float64) float64 {
	sum := 0.0
	// cap the exact sum for very large n to keep generator setup cheap;
	// for n up to a few million this is exact enough.
	limit := n
	if limit > 5_000_000 {
		limit = 5_000_000
	}
	for i := uint64(1); i <= limit; i++ {
		sum += 1.0 / math.Pow(float64(i), theta)
	}
	return sum
}

func (z *zipf) next() uint64 {
	u := z.rng.float64()
	uz := u * z.zetan
	if uz < 1.0 {
		return 0
	}
	if uz < 1.0+math.Pow(0.5, z.theta) {
		return 1
	}
	idx := uint64(float64(z.n) * math.Pow(z.eta*u-z.eta+1.0, z.alpha))
	if idx >= z.n {
		idx = z.n - 1
	}
	// scramble so hot keys are not clustered at the low end of the keyspace
	return (idx*2654435761 + 0x5bd1e995) % z.n
}
