//go:build network_engines

// Package aki adapts tamnd/aki in network mode. aki is a Redis-compatible
// database in a single file: RESP2/RESP3 over a write-ahead-logged, MVCC paged
// storage engine. Its server speaks the Redis flag dialect on purpose
// (--port, --unixsocket, --dir, --appendonly, --appendfsync), so the adapter
// reuses the shared respnet launcher and only prepends aki's "server"
// subcommand to the argv.
//
// aki is on the board as the durable, single-file RESP server: where redis and
// valkey persist through an append-only log replayed on restart, aki keeps a
// paged b-tree store in one file, so it is the closest networked relative to
// what the kv Redis layer is becoming. aki must be on PATH.
//
// aki keys are unordered at the string level like the other RESP engines, so the
// scan workloads skip it.
package aki

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("aki", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "aki",
			Version: "server-on-path",
			Binary:  "aki",
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// aki is a multi-command CLI; the server lives under "aki server".
				return append([]string{"server"}, respnet.RedisDialectArgs(cfg, sock)...)
			},
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "aki's default mirrors Redis: appendonly with appendfsync=everysec, the WAL fsynced about once a second, not per command"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "single-file", Note: "unlike redis and valkey, aki keeps its data in one paged file, so its on-disk footprint is a single store plus its WAL"},
				{Code: "unordered", Note: "the string keyspace has no sorted iteration at the adapter level, so the scan workloads skip aki"},
			},
		})
	})
}
