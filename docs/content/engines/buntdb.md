---
title: "buntdb"
linkTitle: "buntdb"
description: "buntdb is an in-memory B-tree with an append-only file for durability. Fast at almost everything, as long as the whole dataset fits in RAM."
weight: 50
---

**Shape:** in-memory B-tree with append-only persistence, pure Go
**Repository:** [github.com/tidwall/buntdb](https://github.com/tidwall/buntdb)

buntdb keeps the entire dataset in memory in a B-tree and appends changes to a file on disk for durability.
Because every operation hits RAM, it is fast at almost everything: reads, writes, and ordered scans all land near the top of their tables.
The catch is in the description: the whole dataset lives in memory, so it scales with your RAM, not your disk.

## Best at

- **Everything in-memory.** Strong across the board: 3,572,000 reads/sec, 230,000 writes/sec, and the top spot on the 50/50 [mixed](/scenarios/mixed/) workload at 380,000 ops/sec.
- **Predictable space.** 1.0x on disk, fresh writes and updates alike, because the append-only file is compacted to the live set.
- **Ordered scans.** 114,000 keys/sec, supported and reasonable.

## Watch out for

- **RAM-bound.** The dataset must fit in memory. This is the hard limit; past it, buntdb is not the engine.
- **Durable write rate.** 250 per second with a flush on every commit, since each commit appends and flushes.
- **Reads trail the hash-logs.** Fast, but tamnd/kv and pogreb read faster if reads are the entire job.

## Reach for it when

Your dataset comfortably fits in RAM and you want one simple engine that is quick at reads, writes, and scans without tuning.
It is an excellent default for small-to-medium datasets.
Once the data outgrows memory, move to an LSM like [pebble](/engines/pebble/) or [goleveldb](/engines/goleveldb/).
