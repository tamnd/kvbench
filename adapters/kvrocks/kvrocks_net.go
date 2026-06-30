//go:build network_engines

// Package kvrocks adapts Apache Kvrocks in network mode. Kvrocks is a
// Redis-compatible server that keeps its data in RocksDB instead of memory, so
// it speaks RESP over the same go-redis client and unix socket as the other RESP
// engines but persists every write to an LSM on disk. Its command-line flags are
// the Redis-style directive spelling (--dir, --unixsocket, --bind, --port), but
// durability is RocksDB's WAL rather than an append-only file, so it carries its
// own ArgsFn rather than the redis dialect.
//
// Kvrocks is the persistent counterpart that pairs with aki and kv-redis on the
// board: where redis and valkey serve from memory and log to an AOF, Kvrocks
// answers from a RocksDB store, so it is the RESP face on a production LSM. Its
// data path is a directory of SST files plus a WAL, not a single file. The
// kvrocks binary must be on PATH; it is built on the Linux bench host, not the
// macOS laptop the point baseline uses.
//
// Kvrocks string keys are unordered at the adapter level like the other RESP
// engines, so the scan workloads skip it.
package kvrocks

import (
	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kvrocks", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "kvrocks",
			Version: "server-on-path",
			Binary:  "kvrocks",
			Class:   engine.ClassRedisPersistent,
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// Kvrocks listens on the unix socket only when one is set and no
				// address is given explicitly, so passing --unixsocket without
				// --bind/--port keeps it off TCP and clear of parallel runs. --dir
				// is the RocksDB base directory. Durability is the RocksDB WAL: the
				// default buffers it, and FULL asks for a synchronous WAL write per
				// command through the rocksdb.write_options.sync directive.
				args := []string{
					"--dir", cfg.Dir,
					"--unixsocket", sock,
				}
				if cfg.Synchronous == "FULL" {
					args = append(args, "--rocksdb.write_options.sync", "yes")
				}
				return args
			},
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "writes land in RocksDB's WAL, which the default buffers to the OS rather than fsyncing per command, the async-WAL class; FULL switches on rocksdb.write_options.sync for a per-command fsync"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number"},
				{Code: "not-single-file", Note: "Kvrocks stores data as a RocksDB directory of SST files plus a WAL, not a single file, so it is not in the single-file class with kv and aki"},
				{Code: "platform", Note: "benchmarked on the Linux bench host, not the macOS laptop the point baseline is taken on"},
				{Code: "unordered", Note: "the string keyspace has no sorted iteration at the adapter level, so the scan workloads skip kvrocks"},
			},
		})
	})
}
