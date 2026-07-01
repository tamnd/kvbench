# kvbench

An engine-neutral benchmark for embedded and networked key/value stores.
You give it a list of engines and a list of workloads; it runs each engine through the identical harness and writes machine-readable results: throughput, the full latency distribution, and space amplification, with the run environment recorded alongside.

The harness core never imports a concrete engine.
Every store sits behind one adapter interface, so the workload driver, the clock, and the latency histogram cannot tell which engine they are hitting.
That is the point: the comparison is fair because the measurement code is the same for everyone.
There is no home-field engine.

**The results, written for people picking a store, are at [kvbench.tamnd.com](https://kvbench.tamnd.com/).**
That site is scenario-first: name what your workload does most (read-heavy, write-ingest, mixed, durable, scans, footprint) and it tells you which engine to reach for, with real numbers.
This README is for running the benchmark yourself.

tamnd/kv (Spec 2059) is one adapter among many here, with no special treatment.
It is measured as its hash-log storage core, a sharded resident hash index over a hybrid log with an in-memory hot tier and a cold tail spilled to one file, and it runs through the same in-process path as every other embedded engine.
The same core also has a Redis wire face, measured over a socket as `kv-redis` in the network build below.

## Install

```
go install github.com/tamnd/kvbench/cmd/kvbench@latest
```

The default build is pure Go, no cgo, and pulls in the eight embedded engines that run anywhere with zero system dependencies:

| Engine | Family | Notes |
| --- | --- | --- |
| tamnd/kv | hash-log | single-file store, hot/cold hash-log core (Spec 2059) |
| badger | LSM | separate value log |
| pebble | LSM | the engine under CockroachDB |
| goleveldb | LSM | pure-Go LevelDB port |
| bbolt | cow-B+tree | single file, the engine under etcd |
| buntdb | in-memory B-tree | append-only persistence |
| pogreb | hash-log | bitcask-style |
| sqlite | B-tree | pure-Go `modernc.org/sqlite`, used as a KV store |

These eight are the default sweep and the published board.
The harness also carries internal reference rails (an in-memory ceiling and a do-nothing floor) for validating itself; they are marked `reference` in `kvbench list`, left out of the default sweep, and never put in a table next to a real store.
Run them by naming them explicitly if you want the harness's own baselines.

### More engines behind build tags

LMDB, libmdbx, and RocksDB through cgo. Both LMDB and libmdbx bundle their C source, so a C compiler is all they need:

```
CGO_ENABLED=1 go build -tags cgo_engines -o kvbench ./cmd/kvbench
```

RocksDB links the host librocksdb; the cleanest build provisions the exact version grocksdb tracks with its own `build.sh` and points the cgo flags at it:

```
GROCKSDB=$(go list -m -f '{{.Dir}}' github.com/linxGnu/grocksdb)
bash "$GROCKSDB/build.sh" "$HOME/rocksdb-static"
export CGO_CFLAGS="-I$HOME/rocksdb-static/include"
export CGO_LDFLAGS="-L$HOME/rocksdb-static/lib -lrocksdb -lsnappy -llz4 -lz -lzstd"
CGO_ENABLED=1 go build -tags cgo_engines -o kvbench ./cmd/kvbench
```

redb, sled, and fjall in subprocess mode. The harness launches a small Rust helper (`kvbench-rs`, built from `rust/`) and talks to it over a length-prefixed pipe:

```
cargo build --release --manifest-path rust/Cargo.toml
cp rust/target/release/kvbench-rs /usr/local/bin/
go build -tags subprocess_engines -o kvbench ./cmd/kvbench
```

redis, valkey, dragonfly, garnet, kvrocks, and kv's own Redis face (`kv-redis`) in network mode.
Each adapter launches its own RESP server on a per-process unix socket and drives it with go-redis, so there is no Docker and no shared port; a missing server binary marks that engine's cells unsupported rather than failing the run.
kv's server (`go build -o kv ./cmd/kv` in tamnd/kv) speaks the redis-server flag dialect, so the adapter drives it close to the way it drives redis: `--port`, `--unixsocket`, `--dir`, `--appendonly` and `--appendfsync` carry their redis meaning, plus two kv sizing hints (`--value-bytes`, `--cardinality`).
Like every RESP server on the board it is measured at `appendfsync everysec`, the production default for a networked store; the per-commit durable comparison lives on the embedded class.

The go-redis client runs in the harness process next to the server, so on a co-located Linux run pass `--cpu-split` to pin the client and the server to disjoint cores.
Without it the client steals the cores the server needs, unevenly between single-threaded and multi-threaded servers, and the ranking comes out wrong; `--cpu-server` and `--cpu-client` set the core lists by hand, or leave them empty for a balanced split from the core count.

```
go build -tags network_engines -o kvbench ./cmd/kvbench
```

All three primary tags combine in one binary:

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
  --engines kv,bbolt,sqlite,goleveldb,pebble,badger \
  --workloads fillrandom,readrandom,ycsb-a \
  --cardinality 100000 --ops 200000 --conc 8 --reps 3 \
  --out results/mine
kvbench report --in results/mine --md
```

Run `kvbench run` with no `--engines` to sweep every real built-in engine (the reference rails are skipped unless named).
For the portable public profile that every host runs, one DEFAULT pass and one FULL pass so the two durability regimes stay separate, use `make bench-public`; the runner is `scripts/bench-profile.sh` and the fairness model is at [kvbench.tamnd.com/methodology](https://kvbench.tamnd.com/methodology/).

`report` splits the board into four comparison classes and scores them separately, so an in-process get never shares a table with a networked one:
Class 1 embedded local engines, Class 2 Redis-compatible in-memory servers, Class 3 Redis-compatible persistent servers, Class 4 distributed systems.
Each engine carries its class in its metadata, so the split is in the data, not a flag at report time.

## Durability: two regimes, both durable

The same durability word means different things to different engines: bbolt fsyncs on every commit, while badger, pebble, and kv flush in the background.
Comparing one engine's per-commit number against another's background number is the classic benchmark lie, so the axis is explicit and every result carries the regime it ran under.

- `--durability DEFAULT` runs each engine at its own shipped durability, the honest out-of-the-box comparison, every engine exactly as its authors ship it. This is not durability off: the background engines still flush, just on a short timer with a bounded sub-second loss window, the same contract as Redis appendfsync everysec.
- `--durability FULL` forces a per-commit fsync on every embedded engine, so the background engines pay the disk on every write too. This is the real cost of zero-loss durability, measured on one footing. It applies to the embedded class only: the networked RESP servers are always measured at everysec, because a per-commit fsync over a socket is a mode nobody deploys (redis itself calls `appendfsync always` prohibitively slow), so a FULL cell for a networked engine is skipped rather than run under a mislabeled number.

A DEFAULT number and a FULL number never share a table.
That, and the per-engine asterisks that ride along in every result, are what keep a number honest.

## Workloads

YCSB A through F, plus the db_bench staples: fillseq, fillrandom, overwrite, readrandom, readseq, deleterandom.
Keys and values are generated deterministically from a seed (splitmix64, Zipfian where the workload calls for skew), so a run is reproducible.

## Metrics

- Throughput is sustained over the measured window, not a warm-up burst.
- Latency comes from per-client HDR histograms, merged, reported p50 through max, open-loop so a stall lands in the tail (coordinated-omission correction).
- Space amplification is on-disk bytes over logical bytes. Write amplification is taken from engine stats where the engine exposes them.

## Layout

```
engine/      the adapter SPI and registry, imports no concrete engine
adapters/    one package per engine, the only place engine knowledge lives
adapters/inmem/ the harness's own reference rails (the floor and the in-memory ceilings)
rust/        the kvbench-rs helper for subprocess engines (redb, sled, fjall)
workload/    deterministic operation generators (YCSB + db_bench)
hdr/         HDR histogram with coordinated-omission correction
env/         run-environment capture
harness/     the driver: load, measure, collect, emit Result
docs/        the kvbench.tamnd.com site (tago + tago-doks)
cmd/kvbench/ the CLI
```

No `internal/` directories anywhere; every package is importable.
