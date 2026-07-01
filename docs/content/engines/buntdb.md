---
title: "buntdb"
linkTitle: "buntdb"
description: "buntdb is an in-memory B-tree with an append-only file for durability. Fast at almost everything, as long as the whole dataset fits in RAM."
weight: 50
---

**Shape:** in-memory B-tree with append-only persistence, pure Go
**Repository:** [github.com/tidwall/buntdb](https://github.com/tidwall/buntdb)

buntdb keeps the entire dataset in memory in a B-tree and appends changes to a file on disk for durability.
Because every operation hits RAM, it is fast at reads and scans, and its writes have to append and flush to the log so they sit mid-pack.
The catch is in the description: the whole dataset lives in memory, so it scales with your RAM, not your disk.

## Best at

- **In-memory reads.** 3,236,000 reads/sec, second only to tamnd/kv, from a pure in-memory B-tree. See [read-heavy](/scenarios/read-heavy/).
- **Predictable space.** 1.03x on disk, fresh writes and updates alike, because the append-only file is compacted to the live set.
- **Ordered scans.** 98,000 keys/sec, supported and reasonable. See [range scans](/scenarios/range-scans/).

## Watch out for

- **RAM-bound.** The dataset must fit in memory. This is the hard limit; past it, buntdb is not the engine.
- **Writes append and flush.** 16,000 fresh writes/sec, mid-pack, because each commit appends to the log; on the 50/50 [mixed](/scenarios/mixed/) workload it holds 34,000 ops/sec, well behind the hash-log and LSM leaders.
- **Durable write rate.** 99 per second with a flush on every commit, since each commit appends and flushes.
- **Reads trail tamnd/kv.** Fast, but tamnd/kv reads faster if reads are the entire job.

## Reach for it when

Your dataset comfortably fits in RAM and you want one simple engine that is quick at reads, writes, and scans without tuning.
It is an excellent default for small-to-medium datasets.
Once the data outgrows memory, move to an LSM like [pebble](/engines/pebble/) or [goleveldb](/engines/goleveldb/).
