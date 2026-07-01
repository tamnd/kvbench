//go:build network_engines

// Package hlogredis adapts the tamnd/kv hlog engine in network mode. hlog is the
// bare hash-log storage engine (no MVCC, no transaction); its hlog-server binary
// opens one store and speaks the Redis wire protocol over a unix socket, so it
// reaches the harness through the same go-redis client and private socket as the
// other RESP engines.
//
// This is the over-the-wire counterpart to the in-process hlog adapter: the same
// store, the same tier sizing, measured across a socket so the network round-trip
// is in the number rather than left out. Where kv-redis puts a Redis face on kv's
// transactional storage, hlog-redis puts one on the bare engine, so the pair shows
// what the transaction shell costs on the wire.
//
// hlog does not speak the redis flag dialect, so it carries its own ArgsFn rather
// than RedisDialectArgs. The hlog-server binary must be on PATH; build it from
// tamnd/kv with `go build -o hlog-server ./cmd/hlog-server`. It takes the cell's
// value size and cache budget as sizing hints so the served store is the same
// shape as the embedded one, and maps the durability contract onto the engine's
// group commit: OFF leaves the background flusher as the only durability, every
// other level makes the harness Flush a real Sync barrier.
//
// The hash-log keyspace is unordered, so the scan workloads skip it like the
// other RESP engines.
package hlogredis

import (
	"strconv"

	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("hlog-redis", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "hlog-redis",
			Version: "hlog-server-on-path",
			Binary:  "hlog-server",
			Class:   engine.ClassRedisPersistent,
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// The store lives under the cell directory; hlog-server creates it if it
				// is missing. The sizing hints mirror the in-process adapter so the served
				// store is the same shape rather than a differently tuned second instance;
				// the value size sizes the hot segment index and the cache budget sizes the
				// resident cold window, and the server defaults its key index capacity.
				args := []string{
					"--unixsocket", sock,
					"--dir", cfg.Dir,
					"--value-bytes", strconv.Itoa(cfg.ValueBytes),
					"--cache-bytes", strconv.FormatInt(cfg.CacheBytes, 10),
				}
				// Map the durability contract onto the engine's group commit. OFF leaves
				// the background flusher as the only durability; NORMAL and FULL make the
				// Flush hook a real Sync; DEFAULT keeps the engine's synced default.
				switch cfg.Synchronous {
				case "OFF":
					args = append(args, "--synchronous", "off")
				case "NORMAL":
					args = append(args, "--synchronous", "normal")
				case "FULL":
					args = append(args, "--synchronous", "full")
				}
				return args
			},
			// The harness "make it durable now" hook. hlog-server maps BGREWRITEAOF onto
			// a Sync barrier in a synced mode and onto a no-op in the unsynced one, so a
			// Flush at OFF pays no fsync the engine would not otherwise do.
			FlushFn: respnet.FlushAOF,
			Asterisks: []engine.Asterisk{
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate hlog-server process; the round-trip is in the number"},
				{Code: "single-file", Note: "hlog keeps its data in one file plus a sibling commit watermark, so the RESP face's on-disk footprint is a single store"},
				{Code: "group-commit", Note: "a SET returns once it is in the hot tier and the background flusher makes it durable; in a synced mode the Flush hook forces a Sync barrier, so the loss window is bounded by the hot tier rather than per write"},
				{Code: "bare-engine", Note: "this is the hash-log engine behind a Redis front end with no MVCC and no transaction, the over-the-wire counterpart to the in-process hlog adapter"},
				{Code: "unordered", Note: "the hash-log keyspace has no sorted iteration, so the scan workloads skip hlog-redis"},
			},
		})
	})
}
