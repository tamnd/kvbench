//go:build cgo_engines

// Package rocksdb adapts RocksDB, the Facebook/Meta LSM that grew out of
// LevelDB, via the linxGnu/grocksdb cgo binding. grocksdb does not bundle the
// C++ source: it links the system librocksdb, so building this adapter needs
// librocksdb plus its compression libraries on the host (brew install rocksdb,
// or apt install librocksdb-dev). Built only with -tags cgo_engines.
//
// It is the production-grade LSM counterweight in the embedded class: where
// Pebble and goleveldb are pure-Go LSMs, RocksDB is the C++ original that most
// server databases embed, so it sets the bar the Go LSMs are measured against.
// The engine itself lives in adapters/rocksfamily, shared with the Speedb fork;
// this package is just the rocksdb registration and its library-specific notes.
package rocksdb

import (
	"github.com/tamnd/kvbench/adapters/rocksfamily"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("rocksdb", func() engine.Engine {
		return rocksfamily.New("rocksdb", "system-lib", []engine.Asterisk{
			{Code: "system-lib", Note: "links the host librocksdb rather than a bundled copy, so the measured version is whatever the host installed (brew rocksdb on macOS, librocksdb-dev on Debian/Ubuntu)"},
		})
	})
}
