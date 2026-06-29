//go:build network_engines

// Package dragonfly adapts Dragonfly in network mode. Dragonfly is a Redis- and
// Memcached-compatible server built on a shared-nothing, multi-threaded core. It
// speaks RESP, so it reaches the harness through the same go-redis client and
// unix socket as the other RESP engines, but its command-line flags are its own,
// so it carries a dedicated ArgsFn rather than the redis dialect.
//
// Dragonfly persists with periodic snapshots, not an append-only log, and has no
// per-command fsync mode, so the OFF/NORMAL/FULL durability contract does not map
// onto it the way it does onto redis and valkey; every level runs snapshot-only
// and the asterisk says so. The dragonfly binary must be on PATH. There is no
// native macOS build, so this engine runs on Linux (the CI cgo and bench hosts),
// not on the macOS laptop the point baseline is taken on.
//
// Dragonfly keys are unordered at the string level, so the scan workloads skip it.
package dragonfly

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("dragonfly", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "dragonfly",
			Version: "server-on-path",
			Binary:  "dragonfly",
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// Dragonfly disables TCP with --port 0 and listens on the unix
				// socket, keeping the data file under the cell directory. It has no
				// appendonly/appendfsync knobs; durability is snapshot-based, so the
				// contract level does not change the argv.
				return []string{
					"--port=0",
					"--unixsocket", sock,
					"--dir", cfg.Dir,
					"--dbfilename", "dump",
				}
			},
			FlushFn: func(ctx context.Context, cli *goredis.Client) error {
				// SAVE writes a snapshot synchronously, the closest Dragonfly has to
				// "make it durable now".
				return cli.Save(ctx).Err()
			},
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "Dragonfly persists with periodic snapshots and has no per-command fsync, so its durability default is weaker than an append-only log; the OFF/NORMAL/FULL levels all run snapshot-only"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "platform", Note: "Dragonfly has no native macOS build, so this engine runs on Linux only, not on the macOS laptop the point baseline uses"},
				{Code: "unordered", Note: "plain string keys have no sorted iteration, so the scan workloads skip dragonfly"},
			},
		})
	})
}
