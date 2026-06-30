//go:build network_engines

// Package garnet adapts Microsoft Garnet in network mode. Garnet is a
// Redis-compatible cache-store built on the FASTER/Tsavorite log-structured core,
// written in C# on .NET, so it speaks RESP and reaches the harness through the
// same go-redis client and unix socket as the other RESP engines. Its flags are
// its own (--unixsocket, --port, --checkpointdir, --aof), so it carries a
// dedicated ArgsFn rather than the redis dialect.
//
// Garnet sits in the in-memory class next to redis and valkey: it answers from
// memory and persists optionally, through periodic checkpoints and an append-only
// file rather than a per-command sync. Like dragonfly it has no native macOS
// build path the bench uses, so it runs on the Linux bench host, not the macOS
// laptop the point baseline is taken on. The GarnetServer binary must be on PATH,
// which means the .NET build of Garnet published as a self-contained executable.
//
// Garnet string keys are unordered at the adapter level like the other RESP
// engines, so the scan workloads skip it.
package garnet

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("garnet", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "garnet",
			Version: "server-on-path",
			Binary:  "GarnetServer",
			Class:   engine.ClassRedisMemory,
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// --port 0 keeps Garnet off TCP so only the private unix socket is
				// open, and --checkpointdir is where its checkpoints and append-only
				// file live. Garnet answers from memory; OFF runs pure in-memory with
				// no AOF, and NORMAL/FULL turn the append-only file on so the write
				// path carries a log. Garnet has no per-command fsync mode, so FULL
				// and NORMAL both run AOF-on.
				args := []string{
					"--port", "0",
					"--unixsocket", sock,
					"--checkpointdir", cfg.Dir,
				}
				if cfg.Synchronous != "OFF" {
					args = append(args, "--aof")
				}
				return args
			},
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "Garnet answers from memory and persists through periodic checkpoints plus an optional append-only file; it has no per-command fsync, so OFF runs in-memory only and NORMAL/FULL both run with the AOF on, weaker than a per-command sync"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "platform", Note: "Garnet is reached through its .NET GarnetServer build; the bench runs it on the Linux bench host, not the macOS laptop the point baseline uses"},
				{Code: "unordered", Note: "plain string keys have no sorted iteration at the adapter level, so the scan workloads skip garnet"},
			},
		})
	})
}
