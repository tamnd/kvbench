---
title: "Mixed read-update: reads and updates together"
linkTitle: "Mixed read-update"
description: "The typical service workload: reading and updating existing keys in roughly equal measure. buntdb, badger, and pebble keep updates cheap as the data churns."
weight: 30
---

This is the workload most real services actually have: a mix of reading existing keys and updating them, with the traffic skewed toward a hot set (the YCSB-A profile, 50% reads and 50% updates, Zipfian).
User profiles, counters, carts, anything that gets read and written over its lifetime.

The word that matters here is **update**.
Updating an existing key is harder than writing a new one, because the engine has to deal with the old value.
A B-tree overwrites it in place.
An LSM writes a new version and reconciles later.
A hash-log appends the new value and leaves the old one as garbage to be compacted away, and if the same hot keys are updated over and over, that garbage piles up fast.
This is where the read champion stumbles.

## The numbers

50% reads, 50% updates, Zipfian-skewed, 100,000 keys, 1 KB values, 8 concurrent clients:

| Engine | Shape | Ops/sec (M4) | Space used | p99 |
| --- | --- | --- | --- | --- |
| buntdb | in-memory B-tree | **380,000** | 1.0x | 860 us |
| badger | LSM | 307,000 | 22x | 215 us |
| pogreb | hash-log | 304,000 | 1.5x | 1.7 ms |
| pebble | LSM | 225,000 | 0.3x | 235 us |
| goleveldb | LSM | 187,000 | 0.2x | 416 us |
| bbolt | B+tree | 72,000 | 2.3x | 3.2 ms |
| sqlite | B-tree | 35,000 | 4.5x | 2.0 ms |
| tamnd/kv | hash-log | **2,900** | **53x** | 27 ms |

That last row is not a typo.
tamnd/kv reads at nearly 7 million per second in a read-only test, but under a hot-key update mix it falls to 2,900 operations per second and its disk grows to 53x the data size.
Every update appends a new copy and a transaction version, the hot keys are updated thousands of times, and the garbage outruns compaction within the run.
A read-modify-write mix (YCSB-F) is the same picture: tamnd/kv manages 9,100 ops/sec against 468,000 for buntdb.

This is the single most important thing to know about tamnd/kv: it is a read engine.
Point it at a hot-key update workload and it is the slowest engine here by two orders of magnitude.

## What to pick

- **buntdb** if the dataset fits in RAM. It leads this mix and stays at 1.0x space.
- **badger** if you want the lowest tail latency on the mix (215 us p99) and can pay for the disk (22x).
- **pebble** or **goleveldb** for the best balance of speed, a tiny disk footprint, and a controlled tail. These are the safe default for a mixed service workload that has to live on disk.

## What to avoid

- **tamnd/kv** for any update-heavy workload. This is its worst case by far.
- **bbolt** and **sqlite** if the update rate is high; the B-tree page rewrite and their higher tail latency show here.

If your mix is actually 95% reads and 5% updates rather than 50/50, the picture shifts back toward the read engines: at that ratio buntdb (1,200,000), pogreb (1,154,000), and pebble (895,000) lead, and tamnd/kv recovers to 207,000.
The more updates in the mix, the worse tamnd/kv does.
