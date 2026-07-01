---
title: "tamnd/kv"
linkTitle: "tamnd/kv"
description: "tamnd/kv is a single-file embedded store with a hash-log core: a sharded resident hash index over a hybrid log with an in-memory hot tier. It leads reads, writes, and the read-update mix on this board."
weight: 10
---

**Shape:** hash-log, single file, in-memory hot tier over a cold hybrid log
**Repository:** [github.com/tamnd/kv](https://github.com/tamnd/kv)

tamnd/kv is the engine this benchmark was built to keep honest, and it runs through the same adapter as every other store with no special path.
The benchmark measures its hash-log storage core: a sharded resident hash index over a hybrid log, with an in-memory hot tier that absorbs recent writes and a cold tail that spills to a single file.
The key index lives in RAM and the values run larger than memory on disk, the F2-style split that makes point access one index probe and, on a cold-tier hit, one seek.

The headline used to be reads alone.
It is now reads, writes, and the read-update mix, because the hot tier absorbs a write or an update at memory speed and the resident index always points at the newest value.

## Best at

- **Point reads.** 6,955,000 random reads per second on the Apple M4, a 6 microsecond p99, the tightest tail on the board by a wide margin. That is 8.5x the fastest durable competitor. See [read-heavy](/scenarios/read-heavy/).
- **Bulk writes.** Random fill runs at 5,416,000 ops/sec and blind overwrite at 5,352,000, seventeen to twenty-one times the best durable competitor, because a write lands in the hot tier and returns without waiting on the disk. See [write ingest](/scenarios/write-ingest/).
- **Read-update mix.** The YCSB-A 50/50 mix runs at 1,410,000 ops/sec, the fastest engine on the mix, ahead of every durable store. This was the old build's worst case and is now a strength. See [mixed](/scenarios/mixed/).
- **Single-file deployment.** The whole store is one file, easy to copy, back up, or ship.

## Watch out for

- **No ordered scan.** The hash index stores keys unordered, so prefix and range queries are not supported. Rules it out for [scan workloads](/scenarios/range-scans/), which the harness skips for it rather than faking.
- **In-memory key index.** The key index is resident and scales with the number of keys, so a very large keyset needs memory to match. Values spill to disk, the index does not.
- **Out-of-cache reads.** When the working set outgrows the resident cache, a read that misses the hot tier seeks into the cold tail on disk, so the point-read rate drops toward the disk-bound engines. See [read-heavy](/scenarios/read-heavy/) for the out-of-cache numbers.

## Reach for it when

Reads and writes both matter, the key index fits in memory, and you do not need ordered scans.
That is the cache, the read-model store, the session and counter store, the lookup table that also takes a steady write load.
For ordered scans, pick a B-tree or an LSM from this list.

## A note on durability

tamnd/kv is durable in both of its modes; the choice is when the fsync lands, not whether the write survives.

The default flushes on a short timer rather than on every commit: a write lands in the hot tier and returns, and the flusher fsyncs it a moment later.
A crash loses at most the last unflushed sliver, the same bounded-loss contract Redis gives with appendfsync everysec, and this is where the throughput above comes from, because the ack never waits on the disk.

The strict per-commit mode is one option away, `SyncWrites`, and it does not return until the fsync covers the write, so an acked write survives a crash with zero loss.
Concurrent writers coalesce onto a shared fsync, so a burst pays one flush between them rather than one each.
The [durable-writes page](/scenarios/durable-writes/) measures this strict mode against every other engine's per-commit path on the same footing.
Under a per-commit fsync on every write it sustains about 224 durable writes per second on the Apple M4, mid-pack among the engines that flush per commit and well behind the group-committers, the honest cost of trading the hot tier's ack for a disk hit on every write.
