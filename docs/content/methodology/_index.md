---
title: "Methodology"
linkTitle: "Methodology"
description: "How kvbench measures: one harness for every engine, four machines, two durability modes never mixed, and the rules that keep the comparison honest."
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

## The machines

Every workload runs on four machines, so a number is never a single-laptop fluke:

| Label | CPU | Cores | RAM |
| --- | --- | --- | --- |
| Apple M4 | Apple M4 | 10 | 24 GB |
| EPYC 4-core | AMD EPYC | 4 | 6 GB |
| EPYC 6-core | AMD EPYC | 6 | 12 GB |
| EPYC 8-core | AMD EPYC | 8 | 24 GB |

Unless a table says otherwise, the headline number is the Apple M4.
The engines are pure Go with no cgo, so one static binary runs the identical matrix on every host.

## Two durability modes, never mixed

Durability is where benchmarks lie, so kvbench is strict about it.
The same write workload runs in two modes, and the two never share a table:

- **Flush off:** no engine waits on the disk; the OS owns the flush. This measures the structural speed of the engine. Same rules for all.
- **Flush on:** every engine forces a disk flush on every commit. This measures the real cost of durability. Same rules for all.

We never compare engines at their shipped defaults, because those defaults disagree: some flush every commit, some flush on a timer, some not until asked.
Comparing one engine's durable write against another's buffered write is the classic benchmark lie, and refusing to do it is the point of this project.
The [durable-writes scenario](/scenarios/durable-writes/) is built entirely on the flush-on numbers.

## What the metrics mean

- **Throughput** is operations per second, sustained over the measured window after a warm-up, not a peak burst.
- **Latency** is reported as the p99 (and the full distribution in the raw results), measured open-loop at a steady arrival rate so a stall lands in the tail instead of hiding behind the next request.
- **Space amplification** is on-disk bytes divided by logical data bytes, after the workload.

## What this site does not show

kvbench can also measure engines reached through cgo (RocksDB, LMDB, libmdbx), Rust stores over a subprocess, and the Redis-compatible network servers (Redis, Valkey, Dragonfly, and others).
Those run in separate comparison classes, because an in-process `get` and a `get` over a network socket are not the same measurement and should never share a table.

This site focuses on the eight embedded, pure-Go engines, the ones you add with `go get` and run in your own process, because that is the choice most Go developers are actually making.

It also leaves out the internal reference rails the harness carries for its own validation (an in-memory ceiling and a do-nothing floor).
They are useful for checking the harness against itself, and they are not shippable stores, so they never appear on the board.

## Reproduce it

```
go install github.com/tamnd/kvbench/cmd/kvbench@latest
kvbench run --engines kv,badger,pebble,bbolt,buntdb,pogreb,goleveldb,sqlite \
  --durability OFF --workloads readrandom,fillrandom --reps 2
kvbench report --in results/run-1 --md
```

The full matrix that produced these numbers is `scripts/bench-profile.sh` in the repository.
Run it on your own hardware; the whole point is that you do not have to take ours.
