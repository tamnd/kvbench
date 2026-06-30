//go:build speedb_engines

package main

// Speedb is gated behind its own speedb_engines tag, separate from cgo_engines,
// because it and rocksdb both go through the one grocksdb binding that links a
// single librocksdb and so cannot share a binary. Build with -tags speedb_engines
// and point the linker at Speedb's drop-in library; see adapters/speedb and the
// README for the recipe.
import (
	_ "github.com/tamnd/kvbench/adapters/speedb"
)
