# The public profile

This is the benchmark anyone can run and verify.
It fixes every knob that the harness controls so that two people on the same hardware get the same numbers, and two people on different hardware get numbers that differ only by the hardware.
Run it with one command:

```
make bench-public
```

That builds the runner and sweeps the recognized YCSB and db_bench workloads over every built-in engine, then prints a Markdown table.

## Why it is verifiable

Three things are pinned, so nothing in the result depends on a choice you cannot see.

The workload generators are deterministic.
Keys and values come from splitmix64 seeded with `--seed 42`, and the Zipfian skew for the YCSB mixes is seeded the same way, so the exact sequence of operations is fixed.
Change the seed and you change the run; keep it and the run repeats.

The dependency versions are locked.
Every engine is pulled at the version in `go.sum`, including `github.com/tamnd/kv` at a specific commit, so the engines you measure are the engines anyone else measures from the same checkout.

The durability is each engine's shipped default, not a number the harness invents.
See the fairness section below.

What is left is the hardware: the disk's fsync rate, the CPU, the page cache.
The numbers in any result file are indicative of the machine that produced them, and the comparison between engines on that machine is the point, not the absolute ops/s.

## The exact profile

`make bench-public` runs this:

```
kvbench run \
  --workloads fillseq,fillrandom,overwrite,readrandom,readseq,deleterandom,ycsb-a,ycsb-b,ycsb-c,ycsb-d,ycsb-e,ycsb-f \
  --regimes cache-resident \
  --durability DEFAULT \
  --values 1024 --conc 8 --cardinality 100000 --ops 200000 --reps 3 --seed 42 \
  --out results/public
kvbench report --in results/public --md
```

| knob | value | why |
| --- | --- | --- |
| workloads | YCSB A-F + fillseq, fillrandom, overwrite, readrandom, readseq, deleterandom | the two recognized public suites: YCSB for mixed read/write ratios, db_bench for the load and scan staples |
| values | 1024 | the YCSB standard 1 KiB record |
| cardinality | 100000 | a keyspace that fits in cache on a normal machine, so the cache-resident regime measures the engine and not the disk |
| ops | 200000 | each cell measures 200k operations after the load phase, two passes over the keyspace |
| conc | 8 | eight client goroutines over one engine instance, enough to show lock contention without saturating a laptop |
| reps | 3 | three repetitions per cell; the reporter keeps the steady-state figure |
| seed | 42 | fixes the operation sequence |
| durability | DEFAULT | every engine as it ships, see below |

The db_bench workloads are the load and scan shapes: `fillseq` and `fillrandom` insert fresh keys in order and at random, `overwrite` rewrites existing keys, `readrandom` and `readseq` read point and in order, `deleterandom` removes.
The YCSB workloads are the standard mixes: A is 50/50 read/update, B is 95/5 read-heavy, C is read-only, D reads the latest inserts, E is short range scans, F is read-modify-write.

Unordered engines (pogreb, redis, and the kv f2 faces) have no sorted iteration, so the scan workloads (`readseq`, `ycsb-e`) report an error for them instead of a number, by design rather than as a failure.

## Fairness: DEFAULT durability

Forcing every engine to a single durability label is not fair, because the labels do not mean the same thing.
bbolt at NORMAL fsyncs every commit; kv at NORMAL does not.
A table that puts those two side by side under one column heading hides the most important difference between them.

So the public profile runs `--durability DEFAULT`: each engine opens exactly as its library ships, with no durability knob forced by the harness.
That is the honest "what you get when you install it" comparison.
Every cell then carries a `default-durability` asterisk that states what that engine's default actually does, so a fast write number and a slow write number are never compared without the reason attached.

| engine | shipped default | what it costs |
| --- | --- | --- |
| bbolt | fsync the data file on every commit | strongest durability, slowest writes here |
| lmdb | full sync on every commit | same fsync-per-commit class as bbolt |
| libmdbx | SYNC_DURABLE on every commit | full fsync per commit, same class as lmdb |
| sqlite | WAL with synchronous=NORMAL | fsync at checkpoints, not per commit |
| buntdb | SyncPolicy EverySecond | fsync the append file about once a second |
| pogreb | background interval fsync | deferred durability, not per put |
| badger | SyncWrites=false | batches to the value log, fsyncs in the background |
| pebble | WAL not fsynced per commit | background WAL flush |
| goleveldb | WriteOptions Sync=false | log written without per-commit fsync |
| redis | AOF appendfsync=everysec | fsync the append log about once a second |
| kv | SyncFull WAL, the library default | fsync per commit, strongest durability class like bbolt |
| kv-f2-durable | the f2 layout's own per-commit sync | full single-file durability, no WAL or MVCC shell |

To compare engines with durability taken out of the picture entirely, rerun with `--durability OFF`, which removes the per-commit barrier from every engine that has one.
To measure the durability tax on your disk, rerun with `--durability FULL`, where every per-commit-fsync engine converges on the disk's fsync rate.
The contrast between those three runs is in [baseline.md](baseline.md).

## Reading the result

`kvbench report --in results/public --md` tabulates throughput per engine per workload.
Each result JSON also holds the full latency distribution (p50 through max, coordinated-omission corrected) and the read/write/space amplification triple where the engine exposes it.
The `asterisks` array on every cell is the list of caveats that qualify that number; never quote a cell without them.
