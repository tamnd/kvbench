---
title: "Methodology"
linkTitle: "Methodology"
description: "How kvbench measures: one harness for every engine, two durability regimes never mixed, and the rules that keep the comparison honest."
weight: 40
---

The numbers on this site are only worth reading if the method behind them is fair.
Here is the whole method, short enough to read in one sitting, with the [code on GitHub](https://github.com/tamnd/kvbench) to check it against.

## One harness, no home-field engine

The harness core never imports a concrete engine.
Every store sits behind a single adapter interface, so the workload driver, the clock, and the latency histogram cannot tell which engine they are hitting.
When two engines are compared, the only thing that differs is the engine: same loader, same key generator, same measurement code.

## The workloads

Keys and values are generated from a fixed seed, so a run is reproducible.
The workloads are the recognised standards:

- **fillrandom**, **overwrite:** write new keys, then update existing ones.
- **readrandom**, **readseq:** read random keys, then scan keys in order.
- **YCSB A through F:** the standard service mixes, from 50/50 read-update (A) through read-only (C) to read-modify-write (F), Zipfian-skewed so a few hot keys take most of the traffic.

## The machine

The published numbers are from one machine, an Apple M4 laptop (10 cores, 24 GB):

| Label | CPU | Cores | RAM |
| --- | --- | --- | --- |
| Apple M4 | Apple M4 | 10 | 24 GB |

The engines are pure Go with no cgo, so one static binary runs the identical matrix on any host.
A cross-machine re-run under the current methodology is pending; until it lands we publish only the M4 figures rather than a stale multi-host table.

## Two durability regimes, never mixed

Durability is where benchmarks lie, so kvbench is strict about it.
The same write workload runs in two regimes, and the two never share a table:

- **DEFAULT:** every engine runs at its own shipped durability. bbolt and sqlite fsync on every commit; badger, pebble, goleveldb and tamnd/kv acknowledge the write and flush on a short timer, a bounded sub-second loss window, the same contract Redis gives with appendfsync everysec. This is the honest out-of-the-box comparison, and it is where the read and ingest headlines live.
- **FULL:** every engine is forced to flush the disk on every commit. This is the real cost of zero-loss durability, measured on one footing.

The trap kvbench refuses is comparing one engine's per-commit number against another's timer-flush number in the same table.
Both regimes are honest, they answer different questions, and a result always carries the regime it ran under.
The [durable-writes scenario](/scenarios/durable-writes/) is built entirely on the FULL numbers.

## What the metrics mean

- **Throughput** is operations per second, sustained over the measured window after a warm-up, not a peak burst.
- **Latency** is reported as the p99 (and the full distribution in the raw results), measured open-loop at a steady arrival rate so a stall lands in the tail instead of hiding behind the next request.
- **Space amplification** is on-disk bytes divided by logical data bytes, after the workload.

## Classes are never mixed

An in-process `get` and a `get` over a network socket are not the same measurement, so kvbench keeps them in separate comparison classes that never share a table.

Most of this site is the embedded class: the eight pure-Go engines you add with `go get` and run in your own process, because that is the choice most Go developers are actually making.
The Redis-compatible servers (Redis, Valkey, aki, and kv's own wire face) are their own class, measured over a socket at everysec durability on the [Redis-compatible page](/scenarios/redis-compatible/), where the network round-trip is in every number.
kvbench can also drive engines reached through cgo (RocksDB, LMDB, libmdbx) and Rust stores over a subprocess; those are built behind their own tags and are not on the published board yet.

It also leaves out the internal reference rails the harness carries for its own validation (an in-memory ceiling and a do-nothing floor).
They are useful for checking the harness against itself, and they are not shippable stores, so they never appear on the board.

## Reproduce it

```
go install github.com/tamnd/kvbench/cmd/kvbench@latest
kvbench run --engines kv,badger,pebble,bbolt,buntdb,pogreb,goleveldb,sqlite \
  --durability DEFAULT --workloads readrandom,fillrandom --reps 2
kvbench report --in results/run-1 --md
```

The full matrix that produced these numbers is `scripts/bench-profile.sh` in the repository.
Run it on your own hardware; the whole point is that you do not have to take ours.
