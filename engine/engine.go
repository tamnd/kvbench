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
	Version   string       `json:"version"`
	Profile   string       `json:"profile"` // default | tuned
	Caps      Capabilities `json:"caps"`
	Asterisks []Asterisk   `json:"asterisks"`
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
