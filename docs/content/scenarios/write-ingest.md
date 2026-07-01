---
title: "Write ingest: a firehose of new keys"
linkTitle: "Write ingest"
description: "Event logs, metrics, and bulk loads that write new keys fast. tamnd/kv leads by absorbing every write into an in-memory hot tier and spilling to disk in the background."
weight: 20
---

This is the ingest workload: you are writing a stream of new keys as fast as they arrive.
Event logs, metrics pipelines, crawl output, bulk imports.
Reads happen later or elsewhere; right now the only thing that matters is keeping up with the write rate.

An ingest firehose usually writes more data than fits in cache, so these numbers are the out-of-cache case: 300,000 fresh keys against a cache sized well below them, so every engine has to actually put the data on disk, not just in memory.
Each engine runs at its shipped default durability, the honest out-of-the-box comparison.
If every write must instead survive a crash with zero loss, that is a different question with different winners, on the [durable-writes page](/scenarios/durable-writes/).

## The numbers

Writing 300,000 fresh random keys, 1 KB values, 8 concurrent clients, Apple M4:

| Engine | Shape | Writes/sec | Space | p99 |
| --- | --- | --- | --- | --- |
| **tamnd/kv** | hash-log | **1,791,380** | 0.43x | 6 us |
| pebble | LSM | 178,724 | 0.13x | 500 us |
| badger | LSM | 45,790 | 7.41x | 6.3 ms |
| goleveldb | LSM | 43,040 | 0.11x | 7.0 ms |
| pogreb | hash-log | 33,601 | 1.05x | 6.3 ms |
| buntdb | in-memory B-tree | 1,992 | 1.03x | 115 ms |
| sqlite | B-tree | 695 | 4.50x | 204 ms |
| bbolt | B+tree | 96 | 2.28x | 472 ms |

tamnd/kv leads ingest by roughly ten times the next engine, pebble, and the reason is the hot tier.
A write lands in an in-memory append segment and returns; the cold tail is written and compressed in the background, off the acknowledge path.
That is why the write rate does not fall when the data outgrows the cache the way reads do: a write never waits on a seek, it only ever appends to memory.
It also stays compact on disk, 0.43x, because the cold tail is compressed, so it is not trading disk for speed the way badger's 7.41x does here.

bbolt sits at the floor because it fsyncs on every commit even at its default, and a crash-safe B-tree copies a path of pages before each flush.
That is durability cost, not structural slowness, and the [durable-writes page](/scenarios/durable-writes/) is where that trade is measured for everyone on one footing.

## What to pick

- **tamnd/kv** for high-rate ingest where a sub-second worst-case loss window is acceptable, which is most logs, metrics, and derived data. Nothing here ingests faster and it stays compact on disk.
- **pebble** when you also need ordered scans and the smallest on-disk footprint, and can accept a lower raw ingest rate.
- **badger** for a fast LSM ingest path, as long as you can afford its disk footprint until its background GC catches up.

## What to avoid

- **bbolt** and **sqlite** for write-heavy ingest at their defaults. The per-commit fsync and B-tree page rewrite cap them far below the rest.
- Reading any of these rates as zero-loss durable. They are each engine's shipped default; the per-commit-fsync rates are on the [durable-writes page](/scenarios/durable-writes/) and are far lower.
