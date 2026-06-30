//go:build speedb_engines

// Package speedb adapts Speedb, the RocksDB-compatible embedded engine that
// claims a 100 percent drop-in replacement for librocksdb and reworks the
// internals (a redesigned memtable, compaction and write flow) for modern
// storage hardware. Because Speedb keeps the RocksDB C API and ABI, it shares
// the grocksdb-backed engine in adapters/rocksfamily with the rocksdb adapter;
// only the registered name, the version label, and the library the binary links
// differ.
//
// Built only with -tags speedb_engines, a tag the default and cgo_engines builds
// never set, because rocksdb and speedb both go through the one grocksdb binding
// that links a single librocksdb: they cannot coexist in one binary. To build
// speedb, point the linker at Speedb's drop-in library. Speedb's build produces
// libspeedb with the rocksdb symbols, so the usual recipe is to expose it to the
// linker as librocksdb (a symlink, or Speedb's own rocksdb-named build) and set
// CGO_CFLAGS / CGO_LDFLAGS at it, exactly as the rocksdb adapter does for the
// stock library. The README has the full recipe.
//
// Speedb was acquired by Redis in 2024; the open-source engine remains the
// RocksDB-compatible alternative this adapter targets.
package speedb

import (
	"github.com/tamnd/kvbench/adapters/rocksfamily"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("speedb", func() engine.Engine {
		return rocksfamily.New("speedb", "speedb-system-lib", []engine.Asterisk{
			{Code: "rocksdb-compatible", Note: "Speedb is a drop-in RocksDB fork reached through the same grocksdb binding as rocksdb; the API and on-disk format are RocksDB's, the internals (memtable, compaction, write flow) are Speedb's"},
			{Code: "system-lib", Note: "links the host Speedb library exposed to the linker as librocksdb, so the measured version is whatever Speedb build the host provided"},
			{Code: "exclusive-build", Note: "rocksdb and speedb share the one grocksdb binding that links a single librocksdb, so this adapter is built into its own binary under -tags speedb_engines, never alongside rocksdb"},
		})
	})
}
