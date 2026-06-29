//go:build network_engines

// Package kvredis adapts tamnd/kv's Redis (RESP) face in network mode. kv is an
// embedded single-file database whose `kv serve` can also speak the Redis wire
// protocol over a TCP port or a unix socket, so it reaches the harness through
// the same go-redis client and unix socket as the other RESP engines.
//
// This is the engine the whole RESP rail exists to place: redis and valkey are
// the append-log reference, aki the single-file RESP relative, and kv-redis is
// kv's own hash-log storage behind a Redis front end. Every write the harness
// sends is a full kv transaction, so the number carries kv's durability and MVCC,
// not a second storage model bolted onto the wire protocol.
//
// kv does not speak the redis flag dialect, so it carries its own ArgsFn rather
// than RedisDialectArgs: `kv serve <db> --addr "" --resp-unixsocket <sock>` turns
// the HTTP face off and serves only RESP on the private socket, and
// --synchronous maps the durability contract onto kv's WAL. The kv binary must be
// on PATH; build it from tamnd/kv with `go build -o kv ./cmd/kv`.
//
// The RESP face is kv's string keyspace, which is unordered, so the scan
// workloads skip it like the other RESP engines.
package kvredis

import (
	"path/filepath"

	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-redis", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "kv-redis",
			Version: "kv-serve-on-path",
			Binary:  "kv",
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// The database lives under the cell directory; kv serve creates it if
				// it is missing. --addr "" turns the HTTP face off so only the RESP
				// unix socket is open.
				db := filepath.Join(cfg.Dir, "kv.db")
				args := []string{"serve", db, "--addr", "", "--resp-unixsocket", sock}
				// Map the durability contract onto kv's WAL. kv's own default is
				// SyncFull (fsync per commit), so DEFAULT leaves --synchronous unset and
				// kv runs at its shipped durability; OFF drops the per-commit fsync,
				// NORMAL fsyncs at checkpoints, FULL fsyncs every commit.
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
			// No FlushFn: kv commits at the configured sync level on every write, so
			// there is no separate "make it durable now" call to issue.
			Asterisks: []engine.Asterisk{
				{Code: "default-durability", Note: "kv's shipped default is SyncFull, an fsync per commit, the strongest durability class; OFF removes the per-commit barrier and FULL keeps it, so DEFAULT measures kv's own default unlike the everysec RESP engines"},
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate kv serve process; the round-trip is in the number"},
				{Code: "single-file", Note: "kv keeps its data in one file, so the RESP face's on-disk footprint is a single store plus its WAL"},
				{Code: "redis-face", Note: "this is kv's own storage behind a Redis front end; every write is a full kv transaction, so the number carries kv's durability and MVCC rather than a second storage model"},
				{Code: "unordered", Note: "the RESP face is kv's string keyspace with no sorted iteration, so the scan workloads skip kv-redis"},
			},
		})
	})
}
