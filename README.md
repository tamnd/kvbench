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

LMDB and libmdbx through cgo and a C compiler. LMDB uses the PowerDNS binding and libmdbx the
erigontech binding; both bundle the C source, so no system library is needed, only a compiler:

```
CGO_ENABLED=1 go build -tags cgo_engines -o kvbench ./cmd/kvbench
```

redb, sled, and fjall in subprocess mode. These are Rust stores. The harness launches a
small Rust helper (`kvbench-rs`, built from `rust/`) and talks to it over a pipe with a
length-prefixed binary protocol, multiplexed by request id so the clients stay concurrent.
Build the helper once and put it on PATH, then build kvbench with the tag:

```
cargo build --release --manifest-path rust/Cargo.toml
cp rust/target/release/kvbench-rs /usr/local/bin/
go build -tags subprocess_engines -o kvbench ./cmd/kvbench
```

redis in network mode. The adapter launches its own redis-server on a per-process unix
socket, drives it with go-redis, and shuts it down on close, so there is no Docker and no
shared port. redis-server must be on PATH:

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
per-commit fsync floor folded in. 50k keys, 1 KiB values, 200k ops, three reps, on an Apple M4
(10 cores, 24 GB, go1.26.4). Throughput in ops/s, taken from one run so every row is comparable.

| engine | readrandom | fillrandom | class |
| --- | --- | --- | --- |
| devnull | 10,673,924 | 4,075,585 | floor |
| swiss | 5,787,351 | 1,788,770 | ceiling |
| f2 | 4,602,718 | 1,302,264 | ceiling |
| kv-f2 | 4,169,218 | 1,734,763 | ceiling |
| faster | 3,557,711 | 764,614 | ceiling |
| kv | 1,630,025 | 39,729 | durable |
| pogreb | 1,521,420 | 188,809 | durable |
| buntdb | 1,287,873 | 193,575 | durable |
| kv-f2-durable | 1,123,100 | 939,581 | durable |
| bbolt | 771,705 | 33,542 | durable |
| libmdbx | 675,971 | 66,114 | durable, cgo |
| goleveldb | 599,430 | 104,413 | durable |
| badger | 582,571 | 132,698 | durable |
| lmdb | 558,469 | 72,591 | durable, cgo |
| pebble | 525,184 | 139,465 | durable |
| sqlite | 41,638 | 18,744 | durable |

kv ships one core now, f2, a latch-free sharded hash index over a hybrid log, and it shows up
three ways: kv-f2 is the bare core in memory, kv-f2-durable is the durable single-file layout,
and kv is the full DB stack a user gets (WAL, MVCC, transactions). The f2 core reads at 4.2M and
writes at 1.7M, sitting right on the in-memory ceiling, faster on writes than swiss and well past
faster (5.1M reads, 905k writes a generation back behind a single RWMutex). The lock tax that gap
hints at is small at one thread and large under concurrency, which is the next table.

Against the embedded competitors the durable f2 layout is the story. kv-f2-durable writes 940k
and reads 1.1M, while the cgo B+trees it shares the single-file class with, libmdbx and lmdb,
write at 66k and 73k and read at 676k and 558k. A hash-log appends the new value and atomically
repoints one index slot; a copy-on-write B+tree copies a root-to-leaf path of pages on every
commit, so the write gap is the data structure, not the language or the fsync (durability is off
for both). The full kv stack writes at 40k because each benchmark Put is its own WAL'd, MVCC
transaction; that per-commit shell, not the core, is what the kv row measures, and the gap to
kv-f2-durable is its cost.

Under load the latch-free design separates from everything with a lock. Same profile at eight
concurrent clients:

| engine | readrandom | fillrandom |
| --- | --- | --- |
| f2 | 18,599,749 | 5,338,624 |
| kv-f2 | 15,675,158 | 6,614,859 |
| kv-f2-durable | 7,806,464 | 3,851,653 |
| kv | 6,841,222 | 61,266 |
| faster | 8,816,641 | 1,210,484 |
| libmdbx | 1,592,880 | 72,632 |
| lmdb | 436,428 | 77,294 |

The f2 core scales to 15.7M reads and 6.6M writes on eight threads because a read is an atomic
load and a tag probe with no lock, and writers on different shards never touch the same log.
faster, the same store behind one RWMutex, caps at 8.8M reads and 1.2M writes, so f2 reads about
twice as fast and writes about five times as fast: that is the lock tax made visible. The cgo
B+trees scale reads modestly (libmdbx 1.6M, the more modern fork pulling well ahead of lmdb's
436k) and do not scale writes at all, since both serialize every commit on a single writer, the
same wall the WAL'd kv stack hits at 61k. See [docs/baseline.md](docs/baseline.md) for the full
per-workload tables and the durability contrast; turn durability up to FULL and every durable
engine collapses toward the disk's fsync rate, because then the benchmark measures the disk.

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
