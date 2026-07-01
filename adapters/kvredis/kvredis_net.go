//go:build network_engines

// Package kvredis adapts tamnd/kv's Redis (RESP) face in network mode. Since the
// hash-log rewrite, kv is one thing: the bare hash-log storage core behind a Redis
// front end over a single file. The `kv` binary opens one store and answers
// GET/SET/DEL on a TCP port, a unix socket, or both, so it reaches the harness
// through the same go-redis client and private unix socket as redis and valkey.
//
// This is the over-the-wire counterpart to the in-process kv adapter: the same
// store and the same tier sizing, measured across a socket so the network
// round-trip is in the number rather than left out. redis and valkey are the
// append-log reference, aki the single-file RESP relative, and kv-redis is kv's
// own hash-log storage behind a Redis face.
//
// The kv server speaks the redis-server flag dialect, so a benchmark can drive it
// close to the way it drives redis: --port, --bind, --unixsocket, --dir,
// --dbfilename, --appendonly and --appendfsync carry their redis meaning. It also
// takes two kv-specific sizing hints, --value-bytes and --cardinality, so the
// served store is shaped like the in-process one rather than guessing from
// defaults, and --maxmemory sets the resident budget the way it bounds a redis
// instance. The network class is an everysec comparison, so kv-redis runs
// appendfsync everysec: a SET acks from the hot tier and the background flusher
// fsyncs it a moment later, the same bounded sub-second loss window redis and
// valkey run. The per-commit durable comparison lives on the embedded class, not
// over the wire, so the harness never asks a networked engine for FULL. The kv
// binary must be on PATH; build it from tamnd/kv with `go build -o kv ./cmd/kv`.
//
// The hash-log keyspace is unordered, so the scan workloads skip it like the other
// RESP engines.
package kvredis

import (
	"strconv"

	"github.com/tamnd/kvbench/adapters/respnet"
	"github.com/tamnd/kvbench/engine"
)

func init() {
	engine.Register("kv-redis", func() engine.Engine {
		return respnet.New(respnet.Spec{
			Name:    "kv-redis",
			Version: "kv-on-path",
			Binary:  "kv",
			Class:   engine.ClassRedisPersistent,
			ArgsFn: func(cfg engine.Config, sock string) []string {
				// The store lives under the cell directory; kv creates it if it is
				// missing. --port 0 turns the TCP listener off so only the private unix
				// socket is open, the way the redis-dialect servers get --port 0. The
				// sizing hints mirror the in-process adapter so the served store is the
				// same shape rather than a differently tuned second instance: the value
				// size sizes the hot segment, the cardinality sizes the resident key
				// index, and the cache budget sizes the resident cold window.
				args := []string{
					"--port", "0",
					"--unixsocket", sock,
					"--dir", cfg.Dir,
					"--dbfilename", "data.kv",
					"--appendonly", "yes",
					"--value-bytes", strconv.Itoa(cfg.ValueBytes),
					"--cardinality", strconv.FormatUint(cfg.Cardinality, 10),
					"--maxmemory", strconv.FormatInt(cfg.CacheBytes, 10),
				}
				// The network RESP class is an everysec comparison: every server here
				// (redis, valkey, dragonfly, garnet, kvrocks) runs the once-a-second
				// fsync that is the universal production choice, because over a network
				// hop the round-trip dominates and a per-commit fsync on top of it is a
				// mode almost nobody deploys. Redis itself defaults to everysec and
				// documents appendfsync always as prohibitively slow. So kv-redis serves
				// everysec, its shipped default, still durable with a bounded sub-second
				// loss window. The harness never sends FULL here: it skips the per-commit
				// regime for networked engines, so the per-commit durable comparison lives
				// on the embedded class, where the in-process kv adapter measures
				// SyncWrites against bbolt and sqlite on the durable-writes page.
				args = append(args, "--appendfsync", "everysec")
				return args
			},
			// The harness "make it durable now" hook. The kv server maps BGREWRITEAOF
			// onto a Sync barrier, so a Flush forces the group-commit fsync even in the
			// everysec mode where a write would otherwise ack before the disk.
			FlushFn: respnet.FlushAOF,
			Asterisks: []engine.Asterisk{
				{Code: "network-hop", Note: "every op crosses a unix socket to a separate kv process; the round-trip is in the number"},
				{Code: "single-file", Note: "kv keeps its data in one file plus a sibling commit watermark, so the RESP face's on-disk footprint is a single store"},
				{Code: "everysec", Note: "measured at appendfsync everysec, the universal production mode for a networked store: a SET returns once it is in the hot tier and the background flusher fsyncs it a moment later, a bounded sub-second loss window, the same contract redis and valkey run. The per-commit durable comparison lives on the embedded class, not over the wire where the round-trip dominates and appendfsync always is a mode nobody deploys"},
				{Code: "redis-face", Note: "this is kv's bare hash-log storage behind a Redis front end with no transaction, the over-the-wire counterpart to the in-process kv adapter"},
				{Code: "unordered", Note: "the hash-log keyspace has no sorted iteration, so the scan workloads skip kv-redis"},
			},
		})
	})
}
