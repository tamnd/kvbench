# kvbench

An engine-neutral benchmark for embedded and networked key/value stores. You give it a
list of engines and a list of workloads; it runs each engine through the identical harness
and writes machine-readable results: throughput, the full latency distribution, and the
read/write/space amplification triple, with the run environment recorded alongside.

The harness core never imports a concrete engine. Every store sits behind one adapter
interface, so the workload driver, the metric collector, and the reporter cannot tell which
engine they are hitting. That is the point: the comparison is fair because the measurement
code is the same for everyone. There is no home-field engine.

This repository is the competitive counterpart to the `kv` project (Spec 2059). `kv` is one
adapter among many here, with no special treatment.

## Install

```
go install github.com/tamnd/kvbench/cmd/kvbench@latest
```

The default build is pure Go, no cgo, and pulls in the in-process engines that run
anywhere with zero system dependencies: bbolt, goleveldb, pebble, badger, buntdb, pogreb,
SQLite (via the pure-Go `modernc.org/sqlite`), and tamnd/kv. kv ships one core now, f2, a
latch-free sharded hash index over a hybrid log, and it shows up three ways: `kv` is the full
DB stack a user gets (WAL, MVCC, transactions), `kv-f2` is the bare core in memory, and
`kv-f2-durable` is the durable single-file layout. kv is the single-file embedded store this
benchmark exists to keep honest, so it runs through the same in-process path as everyone else.

Alongside the durable engines the default build carries two reference rails, in package
`adapters/inmem`, that are non-durable and not peers to anyone. `devnull` is the floor: it
stores nothing and reads nothing back, so its cell is the harness and dispatch cost every
other result also carries. `swiss` (an open-addressing table), `otter` (a sharded map), and
`faster` (an append log behind a hash index) are point-workload ceilings: the fastest a bare
in-memory structure of that shape serves the same keys, which is the budget a real engine
spends on ordering, persistence and transactions. `memory` is the naive ordered map kept as a
sanity reference.

More engines come in behind build tags, one set per execution mode beyond in-process:

LMDB, libmdbx, and RocksDB through cgo. LMDB uses the PowerDNS binding and libmdbx the
erigontech binding; both bundle the C source, so a C compiler is all they need:

```
CGO_ENABLED=1 go build -tags cgo_engines -o kvbench ./cmd/kvbench
```

RocksDB is the exception: the linxGnu/grocksdb binding links the host librocksdb rather than
bundling it, and grocksdb tracks a specific RocksDB version (10.10.1 at the time of writing), so
the cleanest build provisions that exact version with grocksdb's own `build.sh` and points the
cgo flags at it:

```
GROCKSDB=$(go list -m -f '{{.Dir}}' github.com/linxGnu/grocksdb)
bash "$GROCKSDB/build.sh" "$HOME/rocksdb-static"     # builds librocksdb + compression libs
export CGO_CFLAGS="-I$HOME/rocksdb-static/include"
export CGO_LDFLAGS="-L$HOME/rocksdb-static/lib -lrocksdb -lsnappy -llz4 -lz -lzstd"
CGO_ENABLED=1 go build -tags cgo_engines -o kvbench ./cmd/kvbench
```

The same `build.sh` runs in CI behind a version-keyed cache, so the cgo job pays that build once.

redb, sled, and fjall in subprocess mode. These are Rust stores. The harness launches a
small Rust helper (`kvbench-rs`, built from `rust/`) and talks to it over a pipe with a
length-prefixed binary protocol, multiplexed by request id so the clients stay concurrent.
Build the helper once and put it on PATH, then build kvbench with the tag:

```
cargo build --release --manifest-path rust/Cargo.toml
cp rust/target/release/kvbench-rs /usr/local/bin/
go build -tags subprocess_engines -o kvbench ./cmd/kvbench
```

redis, valkey, dragonfly, aki, and kv-redis in network mode. These are the RESP servers, the
Redis-compatible family. Each adapter launches its own server on a per-process unix socket,
drives it with the pure-Go go-redis client, and shuts it down on close, so there is no Docker
and no shared port. The launch-and-talk plumbing is shared in `adapters/respnet`; an engine is
just a spec naming its binary and flags. redis, valkey and aki speak the same flag dialect;
dragonfly and kv-redis bring their own. The relevant server binary must be on PATH
(`redis-server`, `valkey-server`, `dragonfly`, `aki`, or `kv`); a missing binary marks that
engine's cells unsupported rather than failing the run. aki (tamnd/aki) is the durable
single-file RESP server in the set, the networked relative of the kv Redis layer; kv-redis is
that layer itself, tamnd/kv's `serve` Redis face over its own hash-log store. Dragonfly has no
native macOS build, so it runs on Linux only. The comparison between these engines, and where
kv-redis lands among them, is in [docs/redis-compat.md](docs/redis-compat.md).

```
go build -tags network_engines -o kvbench ./cmd/kvbench
```

All three tags combine in one binary:

```
CGO_ENABLED=1 go build -tags "cgo_engines network_engines subprocess_engines" \
    -o kvbench ./cmd/kvbench
```

## Use

```
kvbench list                          # engines built into this binary
kvbench run [flags]                   # run the matrix, write JSON per cell
kvbench report --in <dir> [--md]      # tabulate a results directory
```

A typical run:

```
kvbench run \
  --engines memory,bbolt,sqlite,goleveldb,pebble,badger \
  --workloads fillrandom,readrandom,ycsb-a \
  --cardinality 100000 --ops 200000 --conc 8 --reps 3 \
  --out results/mine
kvbench report --in results/mine --md
```

Run `kvbench run` with no flags to sweep every built-in engine across every workload at each
engine's shipped durability (`--durability DEFAULT`). For the fixed, reproducible public matrix
that anyone can run and verify, use `make bench-public`; the profile and the fairness model are in
[docs/public-benchmark.md](docs/public-benchmark.md).

`report` splits the board into four comparison classes and scores them separately, so an
in-process get never shares a table with a networked one no matter how many asterisks sit beside
the numbers. Class 1 is the embedded local KV engines (the home division for kv, and the rocksdb,
libmdbx, lmdb, pebble, badger, bbolt and Rust-rail peers); Class 2 is the Redis-compatible
in-memory servers (redis, valkey, dragonfly); Class 3 is the Redis-compatible persistent servers
backed by an on-disk store (aki, kv-redis); Class 4 is the distributed systems under their own
cluster profile. Each engine carries its class in its metadata, so the split is in the data, not a
flag at report time.

## Workloads

YCSB A through F, plus the db_bench staples: fillseq, fillrandom, overwrite, readrandom,
readseq, deleterandom. Keys and values are generated deterministically from a seed
(splitmix64, Zipfian where the workload calls for skew), so a run is reproducible.

## Metrics

- Throughput is sustained over the measured window, not a warm-up burst.
- Latency comes from per-client HDR histograms, merged, reported p50 through max. The
  generator runs open-loop at a constant arrival rate, so a stall lands in the tail instead
  of hiding behind the next request (coordinated-omission correction).
- Space amplification is on-disk bytes over logical bytes. Write amplification is taken from
  engine stats where the engine exposes them, marked unavailable otherwise.

## Baseline

A single-thread point baseline, durability off, so the read and write paths show without the
per-commit fsync floor folded in. 50k keys, 1 KiB values, 100k ops, two reps, on an Apple M4
(10 cores, 24 GB, go1.26.4). Throughput in ops/s, taken from one run so every row is comparable.

| engine | readrandom | fillrandom | class |
| --- | --- | --- | --- |
| devnull | 14,593,389 | 5,226,026 | floor |
| memory | 10,120,682 | 2,191,443 | ceiling |
| otter | 9,473,887 | 2,070,172 | ceiling |
| swiss | 8,581,320 | 2,103,535 | ceiling |
| f2 | 6,440,324 | 1,787,688 | ceiling |
| faster | 6,115,154 | 1,007,670 | ceiling |
| kv-f2 | 5,093,983 | 2,375,275 | ceiling |
| pogreb | 1,784,267 | 184,223 | durable |
| kv | 1,507,836 | 51,637 | durable |
| kv-f2-durable | 1,407,156 | 1,025,207 | durable |
| buntdb | 1,372,601 | 315,763 | durable |
| bbolt | 830,337 | 34,230 | durable |
| libmdbx | 722,336 | 65,957 | durable, cgo |
| badger | 564,899 | 117,668 | durable |
| lmdb | 533,481 | 75,286 | durable, cgo |
| goleveldb | 510,872 | 104,045 | durable |
| pebble | 474,678 | 151,044 | durable |
| rocksdb | 319,011 | 218,651 | durable, cgo |
| sqlite | 49,028 | 27,034 | durable |

kv ships one core now, f2, a latch-free sharded hash index over a hybrid log, and it shows up
three ways: kv-f2 is the bare core in memory, kv-f2-durable is the durable single-file layout,
and kv is the full DB stack a user gets (WAL, MVCC, transactions). The f2 core reads at 5.1M and
writes at 2.4M, sitting right on the in-memory ceiling, faster on writes than swiss and well past
faster (6.1M reads, 1.0M writes behind a single RWMutex). The lock tax that gap hints at is small
at one thread and large under concurrency, which is the next table.

Against the embedded competitors the durable f2 layout is the story. kv-f2-durable writes 1.0M
and reads 1.4M. The fastest-writing durable competitor is rocksdb, an LSM, at 219k, because an
LSM write is an in-memory memtable insert plus a WAL append, no tree rewrite; the cgo cow-B+trees
it shares the single-file class with, libmdbx and lmdb, write slower still at 66k and 75k because
a copy-on-write B+tree copies a root-to-leaf path of pages on every commit. Even the LSM, the
write-friendly shape, is about 5x off the hash-log layout: f2 appends the new value and atomically
repoints one index slot, which is cheaper than both. The gap is the data structure, not the
language or the fsync, since durability is off for all of them. The full kv stack writes at 52k
because each benchmark Put is its own WAL'd, MVCC transaction; that per-commit shell, not the
core, is what the kv row measures, and the gap to kv-f2-durable is its cost.

Under load the latch-free design separates from everything with a lock. Same profile at eight
concurrent clients:

| engine | readrandom | fillrandom |
| --- | --- | --- |
| f2 | 14,127,836 | 3,832,475 |
| kv-f2 | 14,023,840 | 7,706,571 |
| faster | 9,576,491 | 1,023,832 |
| kv | 6,044,366 | 55,029 |
| kv-f2-durable | 5,184,793 | 3,552,932 |
| rocksdb | 1,304,857 | 112,242 |
| libmdbx | 1,126,491 | 61,842 |
| lmdb | 334,493 | 64,183 |

The f2 core scales to 14.0M reads and 7.7M writes on eight threads because a read is an atomic
load and a tag probe with no lock, and writers on different shards never touch the same log.
faster, the same store behind one RWMutex, caps at 9.6M reads and 1.0M writes, so f2 reads about
half again as fast and writes about seven times as fast: that is the lock tax made visible. The
cgo engines scale reads modestly (rocksdb 1.3M off its block cache, libmdbx 1.1M, lmdb 334k) and
do not scale writes at all, since each serializes commits on a single writer or WAL, the same wall
the WAL'd kv stack hits at 55k. See [docs/baseline.md](docs/baseline.md) for the full per-workload
tables and the durability contrast; turn durability up to FULL and every durable engine collapses
toward the disk's fsync rate, because then the benchmark measures the disk.

## Fairness

Every engine declares its family, mode, capabilities, and any asterisks (for example, an
engine whose NORMAL durability is really a full fsync, or one reached through cgo). The
asterisks ride along in every result so a number never ships detached from the caveat that
qualifies it.

Durability is the place fairness is easiest to get wrong, because the same label means
different things to different engines: bbolt at NORMAL fsyncs every commit while kv at NORMAL
does not. So the default run uses `--durability DEFAULT`, which opens each engine exactly as its
library ships and attaches a `default-durability` asterisk stating what that default actually
does. That is the honest out-of-box comparison; `--durability OFF` takes the per-commit barrier
out of every engine to compare write paths directly, and `--durability FULL` measures the
durability tax on the disk. The per-engine default table is in
[docs/public-benchmark.md](docs/public-benchmark.md).

## Layout

```
engine/      the adapter SPI and registry, imports no concrete engine
adapters/    one package per engine, the only place engine knowledge lives
adapters/inmem/ the devnull floor and the in-memory ceilings (swiss, otter, faster, f2)
rust/        the kvbench-rs helper for subprocess engines (redb, sled, fjall)
workload/    deterministic operation generators (YCSB + db_bench)
hdr/         HDR histogram with coordinated-omission correction
env/         run-environment capture
harness/     the driver: load, measure, collect, emit Result
cmd/kvbench/ the CLI
```

No `internal/` directories anywhere; every package is importable.
