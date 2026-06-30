//go:build network_engines

// Package redis adapts Redis in network mode. The adapter owns the server: it
// launches a private redis-server bound to a unix socket inside the cell's data
// directory, talks to it with the pure-Go go-redis client, and shuts it down on
// Close. No Docker and no shared port. redis-server must be on PATH; if it is
// not, Open returns an error and the harness marks the cell unsupported.
//
// The launch and client plumbing lives in adapters/respnet, shared with valkey
// and aki, which speak the same flag dialect. This package is just the Redis
// spec: the binary name, the version it targets, and the asterisks.
//
// Redis string keys are not stored in sorted order, so this adapter is
// unordered and the scan workloads skip it, the same as any hash-indexed store.
package redis

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("redis", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "redis",
			Version: "8.8-on-path",
			Binary:  "redis-server",
			Class:   engine.ClassRedisMemory,
			ArgsFn:  respnet.RedisDialectArgs,
			FlushFn: respnet.FlushAOF,
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "the default is AOF appendonly with appendfsync=everysec: the append log is fsynced about once a second, the Redis out-of-box durability default, not per command"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number, which is the honest cost of a networked store"},
				{Code: "unordered", Note: "plain string keys have no sorted iteration, so the scan workloads skip redis"},
				{Code: "server-version", Note: "the adapter runs whatever redis-server is on PATH; the 8.8 label is the version this board targets, confirm with redis-server --version"},
			},
		})
	})
}
