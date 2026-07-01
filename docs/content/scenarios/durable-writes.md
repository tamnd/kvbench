---
title: "Durable writes: every write must survive a crash"
linkTitle: "Durable writes"
description: "When a write must survive a power cut, the engine flushes the disk on every commit. badger and sqlite win by batching many commits into one flush."
weight: 40
---

This is the workload where the data must not be lost: a ledger, a queue you cannot drop, anything where a power cut in the next second must not erase the write you just acknowledged.

To guarantee that, the engine forces the disk to physically flush before it tells you the write succeeded.
That flush is the most expensive thing a storage engine does, and it changes the ranking completely.
The engines that win the raw [write-ingest](/scenarios/write-ingest/) race are not the ones that win here.

Every engine on this page is run in the same mode: a real disk flush on every single commit.
That is the FULL durability regime, same rules for everyone, so the numbers are directly comparable.
This is the honest cost of durability, and it is why these figures are in the tens to low thousands rather than the millions.

## The numbers

Durable writes, flush on every commit, 1 KB values, 8 concurrent clients, on the Apple M4:

| Engine | Shape | Durable writes/sec | p99 | How |
| --- | --- | --- | --- | --- |
| badger | LSM | **4,676** | 14 ms | Groups concurrent commits into one flush |
| sqlite | B-tree | **2,152** | 53 ms | Groups concurrent commits into one flush |
| goleveldb | LSM | 463 | 104 ms | One flush per commit |
| pebble | LSM | 383 | 109 ms | One flush per commit |
| tamnd/kv | hash-log | 224 | 202 ms | One flush per commit |
| pogreb | hash-log | 158 | 264 ms | One flush per commit |
| buntdb | in-memory B-tree | 99 | 422 ms | One flush per commit |
| bbolt | B+tree | 52 | 476 ms | One flush per commit |

The gap at the top is not a faster disk, it is a smarter commit.
**badger** and **sqlite** practice group commit: when eight clients commit at once, the engine collects them and flushes the disk once for the whole batch, so eight durable writes cost one flush.
The other engines flush per commit, so they hit the disk's physical flush ceiling, a few hundred per second on this hardware, no matter how many clients are waiting.

This is the one place where tamnd/kv's per-commit fsync shows: at 224 durable writes/sec it sits mid-pack, well behind the group-committing engines.
Its strict mode does coalesce concurrent writers onto a shared fsync, so a burst pays one flush between them, but it does not batch as aggressively as badger and sqlite, and this workload measures exactly that gap.
If durable write throughput under concurrency is your bottleneck, that batching is the feature to look for.

## What to pick

- **badger** for durable writes with the lowest tail (14 ms p99) and an LSM's friendly write path.
- **sqlite** if you also want SQL and transactions; it is the second-fastest here through the same group-commit trick.
- Either one any time many clients commit concurrently and every commit must be safe.

## What to avoid

- **bbolt** for high-rate durable writes. At 52 per second it is the floor here, because a crash-safe B-tree copies a path of pages and then flushes on every commit.
- Reading a background-flush write number (from the [ingest page](/scenarios/write-ingest/)) as if it were per-commit durable. An engine doing millions of writes/sec on a background flush does a few hundred to a few thousand with a flush on every commit. Always check which regime a write number is.

## A note on the two regimes

Out of the box, these engines disagree about durability: some flush on every commit, some flush on a short timer, none of them lose data outright.
That is why kvbench measures two regimes and never mixes them in one table.

DEFAULT runs each engine at its own shipped durability, the honest out-of-the-box comparison.
The timer-flush engines, badger, pebble, goleveldb and tamnd/kv, acknowledge a write before the disk has it and flush a moment later, so a crash loses at most a bounded sub-second sliver, the same contract Redis gives with appendfsync everysec.
This page is the other regime, FULL, where every engine is forced to flush on every commit so the disk is in the loop for all of them equally.
tamnd/kv is durable in both: its default trades a sub-second worst-case loss window for the throughput on the [ingest page](/scenarios/write-ingest/), and its `SyncWrites` mode gives the zero-loss per-commit guarantee measured here.
The [methodology](/methodology/) explains how the two regimes are kept comparable.
