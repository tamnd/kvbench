//go:build network_engines && legacy_engines

// Package legacykeydb adapts KeyDB, kept only as a labeled historical rail. KeyDB
// is a multithreaded Redis fork whose last release is 6.3.4 and whose maintenance
// has gone quiet, so the 2026 refresh demotes it: it is not a headline competitor
// and its numbers should not stand in for the actively maintained Redis-compatible
// ecosystem. Use redis, valkey, dragonfly, and the Class 3 persistent servers for
// that.
//
// It is double-gated behind network_engines AND legacy_engines, so a normal
// network build does not compile it; you opt in with
// `-tags "network_engines legacy_engines"`. KeyDB speaks the redis flag dialect,
// so it reuses the shared respnet launcher and RedisDialectArgs unchanged.
// keydb-server must be on PATH.
//
// It registers as legacy-keydb, not keydb, so the name on the board carries the
// demotion. KeyDB string keys are unordered, so the scan workloads skip it.
package legacykeydb

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("legacy-keydb", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "legacy-keydb",
			Version: "6.3.4-on-path",
			Binary:  "keydb-server",
			Class:   engine.ClassRedisMemory,
			ArgsFn:  respnet.RedisDialectArgs,
			FlushFn: respnet.FlushAOF,
			Asterisks: []engine.Asterisk{
				{Code: "legacy", Note: "KeyDB is retained only as a legacy Redis-compatible target; its last release is 6.3.4 and maintenance has gone quiet, so its result does not represent the actively maintained Redis-compatible ecosystem in 2026"},
				{Code: "default-durability", Note: "KeyDB inherits the Redis default: AOF appendonly with appendfsync=everysec, the append log fsynced about once a second, not per command"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "unordered", Note: "plain string keys have no sorted iteration, so the scan workloads skip legacy-keydb"},
				{Code: "server-version", Note: "the adapter runs whatever keydb-server is on PATH; the 6.3.4 label is the last release this board targets, confirm with keydb-server --version"},
			},
		})
	})
}
