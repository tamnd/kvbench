//go:build cgo_engines

package main

// Heavy adapters that need a C toolchain are compiled in only under the
// cgo_engines build tag, keeping the default build pure-Go and dependency-free.
import _ "github.com/tamnd/kvbench/adapters/lmdb"
