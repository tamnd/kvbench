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
Same rules for everyone, so the numbers are directly comparable.
This is the honest cost of durability, and it is why these figures are in the hundreds and low thousands rather than the hundreds of thousands.

## The numbers

Durable writes, flush on every commit, 1 KB values, 8 concurrent clients, on the Apple M4:

| Engine | Shape | Durable writes/sec | p99 | How |
| --- | --- | --- | --- | --- |
| sqlite | B-tree | **17,000** | 4 ms | Groups concurrent commits into one flush |
| badger | LSM | **16,000** | 2 ms | Groups concurrent commits into one flush |
| goleveldb | LSM | 1,100 | 31 ms | One flush per commit |
| pebble | LSM | 980 | 32 ms | One flush per commit |
| tamnd/kv | hash-log | 740 | 58 ms | One flush per commit |
| pogreb | hash-log | 360 | 102 ms | One flush per commit |
| buntdb | in-memory B-tree | 250 | 105 ms | One flush per commit |
| bbolt | B+tree | 110 | 230 ms | One flush per commit |

The 20x gap at the top is not a faster disk, it is a smarter commit.
**badger** and **sqlite** practice group commit: when eight clients commit at once, the engine collects them and flushes the disk once for the whole batch, so eight durable writes cost one flush.
The other engines flush per commit, so they hit the disk's physical flush ceiling, a few hundred per second on this hardware, no matter how many clients are waiting.

This is the one place where tamnd/kv's per-commit fsync shows: at 740 durable writes/sec it sits mid-pack, well behind the group-committing engines.
If durable write throughput under concurrency is your bottleneck, that batching is the feature to look for.

## What to pick

- **badger** for durable writes with the lowest tail (2 ms p99) and an LSM's friendly write path.
- **sqlite** if you also want SQL and transactions; it matches badger's durable rate through the same group-commit trick.
- Either one any time many clients commit concurrently and every commit must be safe.

## What to avoid

- **bbolt** for high-rate durable writes. At 110 per second it is the floor here, because a crash-safe B-tree copies a path of pages and then flushes on every commit.
- Reading a flush-off write number (from the [ingest page](/scenarios/write-ingest/)) as if it were durable. An engine doing 239,000 writes/sec with the flush off does a few hundred to a few thousand with it on. Always check which mode a write number is.

## A note on defaults

Out of the box, these engines disagree about durability: some flush on every commit, some flush on a timer, some not at all until you ask.
That is why kvbench never compares them at their shipped defaults.
tamnd/kv, for example, ships a default that flushes on a short timer and at checkpoints rather than on every commit, trading a sub-second worst-case loss window for far better throughput, with a per-commit-flush mode one option away when you need the numbers on this page.
The [methodology](/methodology/) explains how the modes are kept comparable.
