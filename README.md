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
SQLite (via the pure-Go `modernc.org/sqlite`), and all three cores of tamnd/kv as
`kv-btree`, `kv-lsm`, and `kv-betree`. kv is the single-file embedded store this benchmark
exists to keep honest, so it runs through the same in-process path as everyone else. The
`kv-betree` core is the 2059-redesign Bε-tree, off by default inside kv and benchmarked here
only because it is the core under active work, so its numbers are a moving target.

Alongside the durable engines the default build carries two reference rails, in package
`adapters/inmem`, that are non-durable and not peers to anyone. `devnull` is the floor: it
stores nothing and reads nothing back, so its cell is the harness and dispatch cost every
other result also carries. `swiss` (an open-addressing table), `otter` (a sharded map), and
`faster` (an append log behind a hash index) are point-workload ceilings: the fastest a bare
in-memory structure of that shape serves the same keys, which is the budget a real engine
spends on ordering, persistence and transactions. `memory` is the naive ordered map kept as a
sanity reference.

Three more engines come in behind build tags, one per execution mode beyond in-process:

LMDB through cgo and a C compiler. It uses the PowerDNS binding, which bundles the LMDB C
source, so no system liblmdb is needed:

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
per-commit fsync floor folded in. 50k keys, 1 KiB values, on a darwin/arm64 laptop.
Throughput in ops/s.

| engine | readrandom | fillrandom | class |
| --- | --- | --- | --- |
| devnull | 14,700,000 | 5,080,000 | floor |
| swiss | 8,730,000 | 2,160,000 | ceiling |
| otter | 8,260,000 | 2,090,000 | ceiling |
| memory | 6,900,000 | 1,830,000 | ceiling |
| faster | 5,140,000 | 905,000 | ceiling |
| pogreb | 1,710,000 | 170,000 | durable |
| buntdb | 1,430,000 | 251,000 | durable |
| bbolt | 871,000 | 45,000 | durable |
| kv-lsm | 780,000 | 99,000 | durable |
| kv-btree | 766,000 | 22,000 | durable |
| goleveldb | 604,000 | 117,000 | durable |
| kv-betree | 584,000 | 6,100 | durable |
| badger | 571,000 | 168,000 | durable |
| pebble | 481,000 | 155,000 | durable |
| sqlite | 51,000 | 28,000 | durable |

The in-memory read ceiling here is about 8.7M ops/s (swiss); the fastest durable engine reads
at roughly a ninth of that, and the gap is the price of an ordered, persistent, transactional
structure over a bare hash table. devnull at 14.7M is the harness floor no engine can beat in
this harness, because the rest of a cell is generating the op and recording its latency.

The write column is the no-fsync write-path cost. Turn durability up to FULL and every durable
engine collapses toward the disk's fsync rate, a few hundred commits a second on this machine,
because then the benchmark measures the disk rather than the engine. That is the answer to
"why is the write benchmark so slow": at FULL it is fsync-bound by design. Run OFF to see the
engine, FULL to see the durability tax. See [docs/baseline.md](docs/baseline.md) for the full
per-workload tables and the durability contrast.

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
adapters/inmem/ the devnull floor and the in-memory ceilings (swiss, otter, faster)
rust/        the kvbench-rs helper for subprocess engines (redb, sled, fjall)
workload/    deterministic operation generators (YCSB + db_bench)
hdr/         HDR histogram with coordinated-omission correction
env/         run-environment capture
harness/     the driver: load, measure, collect, emit Result
cmd/kvbench/ the CLI
```

No `internal/` directories anywhere; every package is importable.
