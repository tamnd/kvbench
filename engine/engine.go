// Package engine defines the kvbench Engine SPI: the single, small interface
// every key/value store under test implements. Everything above this seam
// (the workload driver, the latency collector, the reporter) is engine-blind.
// Nothing in this package imports a concrete database; concrete engines live
// only in adapters/<name> and register themselves through Register.
package engine

import "context"

// Family is the storage architecture of an engine.
type Family string

const (
	FamilyBTree    Family = "btree"     // in-place B+tree (SQLite)
	FamilyCOWBTree Family = "cow-btree" // copy-on-write B+tree (bbolt, LMDB)
	FamilyLSM      Family = "lsm"       // log-structured merge (Badger, Pebble, LevelDB)
	FamilyHashLog  Family = "hash-log"  // bitcask / log-structured hash (NutsDB)
	FamilyInMemory Family = "in-memory" // non-durable reference (map)
)

// Mode is how the adapter reaches its store.
type Mode string

const (
	ModeInProc     Mode = "in-proc"    // Go library linked directly
	ModeCgo        Mode = "cgo"        // C/C++ library via cgo
	ModeSubprocess Mode = "subprocess" // non-Go binary over a pipe protocol
	ModeNetwork    Mode = "network"    // server over a wire protocol
)

// Class is the comparison division an engine belongs to. The published
// leaderboard is split by class so an in-process get never shares a board with
// a networked get, no matter how many asterisks sit beside the numbers. The
// four classes follow Spec 2059 bench doc 12 section 3.
type Class string

const (
	// ClassEmbedded is Class 1: a local KV engine linked into the process, or
	// reached over a pipe to a co-located helper. The home division for kv.
	ClassEmbedded Class = "embedded"
	// ClassRedisMemory is Class 2: a Redis-compatible server whose keyspace is a
	// RAM hash table, with persistence as an append log replayed on restart.
	ClassRedisMemory Class = "redis-memory"
	// ClassRedisPersistent is Class 3: a Redis-compatible server backed by an
	// on-disk store, so a read or write touches that store and the data set can
	// outgrow RAM.
	ClassRedisPersistent Class = "redis-persistent"
	// ClassDistributed is Class 4: a distributed KV system measured under a
	// separate cluster profile, never on a board with the embedded engines.
	ClassDistributed Class = "distributed"
)

// ClassOf returns the comparison class for a meta, deriving a default when the
// adapter did not set one. Anything not reached over the network is an embedded
// engine; a network engine that does not name its class is treated as a
// Redis-compatible in-memory server, the common case.
func ClassOf(m Meta) Class {
	if m.Class != "" {
		return m.Class
	}
	if m.Mode == ModeNetwork {
		return ClassRedisMemory
	}
	return ClassEmbedded
}

// Capabilities declares what an engine can do. The driver uses it to decide
// which workloads apply (an unordered engine skips range scans), and the
// reporter uses it for the capability matrix.
type Capabilities struct {
	Ordered      bool // supports ordered Scan
	AtomicBatch  bool // batch Commit is atomic
	Durable      bool // can fsync on commit
	Transactions bool // MVCC / serializable available
	OnlineBackup bool // copy-while-open supported
	SingleFile   bool // one data file (the kv/bbolt/LMDB property)
	PureNoCgo    bool // no cgo / no native toolchain to build
}

// Asterisk is a fairness caveat that travels with every result.
type Asterisk struct {
	Code string `json:"code"`
	Note string `json:"note"`
}

// Meta identifies an engine for reporting and fairness.
type Meta struct {
	Name      string       `json:"name"`
	Family    Family       `json:"family"`
	Mode      Mode         `json:"mode"`
	Class     Class        `json:"class"` // comparison division; ClassOf fills a default when empty
	Version   string       `json:"version"`
	Profile   string       `json:"profile"` // default | tuned
	Caps      Capabilities `json:"caps"`
	Asterisks []Asterisk   `json:"asterisks"`
	// Reference marks an engine that is not a peer to the real stores: an
	// in-memory ceiling, the devnull floor, or a bare kv core shown without its
	// DB shell. Reference engines stay runnable by name but are left out of the
	// default sweep and the published board so they cannot be mistaken for a
	// shippable store.
	Reference bool `json:"reference,omitempty"`
}

// Config is passed to Open. Embedded engines use Dir; network engines use Addr.
type Config struct {
	Dir         string            // clean data directory for embedded modes
	Addr        string            // server address for network mode
	Profile     string            // "default" | "tuned"
	Synchronous string            // "DEFAULT" | "OFF" | "NORMAL" | "FULL"
	CacheBytes  int64             // target cache size for the tuned profile
	ValueBytes  int               // hint for value sizing
	Extra       map[string]string // engine-specific tuning (from the profile file)
}

// Stats is whatever the engine exposes for RUM extraction. Missing fields are
// reported as -1 ("unavailable"), never guessed.
type Stats struct {
	BytesWritten   int64 // logical/engine-reported bytes written, -1 if unknown
	BytesRead      int64 // -1 if unknown
	OnDiskBytes    int64 // data dir size if the harness can compute it, else -1
	NumFiles       int64 // -1 if unknown
	CacheHits      int64 // -1 if unknown
	CacheMisses    int64 // -1 if unknown
	CompactionRuns int64 // -1 if unknown
}

func UnknownStats() Stats {
	return Stats{-1, -1, -1, -1, -1, -1, -1}
}

// Batch groups writes into one commit (atomic where the engine supports it).
type Batch interface {
	Put(key, value []byte)
	Delete(key []byte)
	Commit(ctx context.Context) error
	Len() int
}

// Iterator is a forward cursor positioned by Scan.
type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// Engine is a mounted key/value store under test. One instance per run.
type Engine interface {
	Meta() Meta
	Open(ctx context.Context, cfg Config) error
	Get(ctx context.Context, key []byte) (value []byte, found bool, err error)
	Put(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	NewBatch() Batch
	Scan(ctx context.Context, start []byte) (Iterator, error)
	Flush(ctx context.Context) error
	Stats(ctx context.Context) (Stats, error)
	Close(ctx context.Context) error
}
