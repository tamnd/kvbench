---
title: "Mixed read-update: reads and updates together"
linkTitle: "Mixed read-update"
description: "The typical service workload: reading and updating existing keys in roughly equal measure. tamnd/kv leads it, because the hot tier absorbs an update at write speed."
weight: 30
---

This is the workload most real services actually have: a mix of reading existing keys and updating them, with the traffic skewed toward a hot set (the YCSB-A profile, 50% reads and 50% updates, Zipfian).
User profiles, counters, carts, anything that gets read and written over its lifetime.

The word that matters here is **update**.
Updating an existing key is harder than writing a new one, because the engine has to deal with the old value.
A B-tree overwrites it in place and rewrites the page.
An LSM writes a new version and reconciles later.
A hash-log with an in-memory hot tier takes the update straight into memory, points its resident index at the new value, and lets the old copy fall away as the cold tail is rewritten, so a hot key that is updated a thousand times costs a thousand cheap memory writes and no disk work in the ack path.

## The numbers

50% reads, 50% updates, Zipfian-skewed, 100,000 keys, 1 KB values, 8 concurrent clients, Apple M4:

| Engine | Shape | Ops/sec | Space | p99 |
| --- | --- | --- | --- | --- |
| tamnd/kv | hash-log | **1,410,061** | **0.01x** | **1.7 us** |
| pebble | LSM | 384,843 | 0.21x | 251 us |
| badger | LSM | 271,031 | 22x | 203 us |
| pogreb | hash-log | 38,804 | 1.06x | 12.6 ms |
| buntdb | in-memory B-tree | 34,745 | 1.03x | 12.6 ms |
| goleveldb | LSM | 32,243 | 0.12x | 8.3 ms |
| sqlite | B-tree | 6,999 | 4.50x | 14.0 ms |
| bbolt | B+tree | 101 | 2.33x | 474 ms |

tamnd/kv leads the mix, at 1.4 million ops/sec against 385,000 for the next engine, pebble, with a 1.7 microsecond p99, a tail three orders of magnitude tighter than the LSM stores.
The hot tier is why: an update lands in memory at the same rate as a fresh write, the resident index is repointed, and nothing in the acked path touches the disk.
The 0.01x space figure in the table is the cache-resident case, where the values are still held in the hot tier and little has spilled, so it flatters the footprint; the honest steady-state on-disk number, measured with the data actually on the platter, is 0.43x on the [footprint page](/scenarios/footprint/), still compact and nothing like the old build's churn.
A read-modify-write mix (YCSB-F) is the same shape: tamnd/kv runs 1,792,000 ops/sec against 732,000 for pebble.

The lead over the durable field is real but it is not a flat five times on this workload.
pebble's LSM is genuinely good at a read-update mix, so tamnd/kv is ahead by 3.7x on YCSB-A and 2.5x on YCSB-F, clear wins under the disk-bound engines but closer than the read-only and bulk-write gaps.

## What to pick

- **tamnd/kv** if the keyset fits in memory. It leads the mix, keeps the tightest tail, and stays compact on disk.
- **pebble** if the dataset outgrows memory and you want an LSM that stays strong on updates with a controlled tail and a small footprint.
- **badger** if you want a mature LSM and can pay the disk for its low tail latency; under this churny mix it sat at 22x the data before its GC caught up.

## What to avoid

- **bbolt** and **sqlite** if the update rate is high; the per-commit fsync and the B-tree page rewrite show up as tail latency in the tens to hundreds of milliseconds.
- **buntdb** past the point the dataset fits in RAM, since it holds everything in memory.

If your mix is actually 95% reads and 5% updates rather than 50/50 (the YCSB-B profile), tamnd/kv pulls further ahead, to 7,808,000 ops/sec against 588,000 for pebble, because there are fewer writes to spill and almost all the traffic is the index probe it is fastest at.
