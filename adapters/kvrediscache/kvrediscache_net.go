//go:build network_engines

// Package kvrediscache adapts tamnd/kv's Redis (RESP) face running in memory, the
// in-memory-cache half of the kv Redis story. It is the same binary and the same
// wire loop as the kv-redis adapter, but `kv serve :memory:` opens the database on
// kv's in-memory backend instead of a file, so nothing touches the disk and the
// store is a pure RAM cache. That puts kv in Class 2 next to redis, valkey,
// dragonfly and garnet, the in-memory servers, while the kv-redis adapter keeps kv
// in Class 3 next to the persistent ones.
//
// The point of having both is that it is one engine and one Redis face measured as
// a cache and as a durable store: kv-redis-cache shows what kv's hash-log does with
// the disk out of the path, and kv-redis shows what a full committed write costs.
// The kv binary must be on PATH; build it from tamnd/kv with `go build -o kv ./cmd/kv`.
//
// The RESP face is kv's string keyspace, which is unordered, so the scan workloads
// skip it like the other RESP engines.
package kvrediscache

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-redis-cache", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "kv-redis-cache",
			Version: "kv-serve-on-path",
			Binary:  "kv",
			Class:   engine.ClassRedisMemory,
			ArgsFn: func(_ engine.Config, sock string) []string {
				// :memory: opens kv on its in-memory backend: no file, the store gone
				// when the process exits. --addr "" turns the HTTP face off so only the
				// RESP unix socket is open. --synchronous off removes the per-commit
				// group-commit barrier: an in-memory cache has no durability to provide,
				// so it runs with the write barrier off the way the other Class 2
				// servers do, rather than at kv's SyncFull default which would make it
				// pay for a durability it cannot deliver from RAM. The cell's own
				// durability level is ignored on purpose; this entry is the cache.
				return []string{"serve", ":memory:", "--addr", "", "--resp-unixsocket", sock, "--synchronous", "off"}
			},
			// No FlushFn: an in-memory cache has nothing to flush to disk.
			Asterisks: []engine.Asterisk{
				{Code: "in-memory", Note: "kv serve :memory: opens the database on kv's in-memory backend, so this is a pure RAM cache with no disk in the path; nothing is persisted and the store is gone when the process exits, the same ephemeral contract as redis or valkey with persistence off"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate kv serve process; the round-trip is in the number"},
				{Code: "redis-face", Note: "this is kv's own storage behind a Redis front end; the durable counterpart is the kv-redis adapter, which runs the identical wire loop over an on-disk database in Class 3"},
				{Code: "unordered", Note: "the RESP face is kv's string keyspace with no sorted iteration, so the scan workloads skip kv-redis-cache"},
			},
		})
	})
}
