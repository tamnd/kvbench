//go:build network_engines

// Package valkey adapts Valkey, the Linux Foundation fork of Redis, in network
// mode. Valkey is wire- and flag-compatible with Redis, so the adapter is just a
// spec over the shared respnet launcher: it launches valkey-server on a private
// unix socket, drives it with go-redis, and shuts it down on Close. valkey-server
// must be on PATH.
//
// Valkey and Redis share a keyspace model, so this adapter is unordered too and
// the scan workloads skip it.
package valkey

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("valkey", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "valkey",
			Version: "9.1-on-path",
			Binary:  "valkey-server",
			Class:   engine.ClassRedisMemory,
			ArgsFn:  respnet.RedisDialectArgs,
			FlushFn: respnet.FlushAOF,
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "valkey inherits the Redis default: AOF appendonly with appendfsync=everysec, the append log fsynced about once a second, not per command"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "unordered", Note: "plain string keys have no sorted iteration, so the scan workloads skip valkey"},
				{Code: "server-version", Note: "the adapter runs whatever valkey-server is on PATH; the 9.1 label is the version this board targets, confirm with valkey-server --version"},
			},
		})
	})
}
