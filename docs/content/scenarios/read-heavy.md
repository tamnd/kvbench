---
title: "Read-heavy: mostly reading keys you already wrote"
linkTitle: "Read-heavy"
description: "Caches, lookup tables, and read-mostly indexes. tamnd/kv reads at nearly 7 million keys per second, ahead of every other embedded Go engine measured."
weight: 10
---

This is the cache and lookup-table workload: you write the data once (or rarely), and after that the job is almost entirely `get`.
Session stores, feature flags, denormalised read models, anything that sits in front of a slower system of record.

When reads dominate, the engine's storage shape barely matters except for one thing: how few steps a `get` takes.
The hash-log engines win here because a read is one in-memory index lookup and, on a miss in the hot tier, one seek into the cold tail, nothing more.

## The numbers

Pure random reads, 100,000 keys, 1 KB values, 8 concurrent clients, on the Apple M4:

| Engine | Shape | Reads/sec | p99 latency |
| --- | --- | --- | --- |
| **tamnd/kv** | hash-log | **6,955,287** | 6 us |
| buntdb | in-memory B-tree | 3,235,921 | 27 us |
| pogreb | hash-log | 1,748,194 | 13 us |
| goleveldb | LSM | 815,535 | 198 us |
| pebble | LSM | 713,196 | 241 us |
| bbolt | B+tree | 698,490 | 244 us |
| badger | LSM | 560,743 | 451 us |
| sqlite | B-tree | 52,394 | 1.7 ms |

tamnd/kv reads faster than everything else here, with the tightest tail by a wide margin: a 6 microsecond p99 against 198 microseconds for the first LSM.
That gap is the hash-log shape doing what it is built for.

The size of the lead depends on what you compare against, and the honest split matters.
Against the durable on-disk stores, badger, pebble, goleveldb, bbolt and sqlite, tamnd/kv reads 8.5x faster than the quickest of them.
Against the two read-first neighbours, the in-memory buntdb and the on-disk hash-log pogreb, the lead is about two times, not eight, because they share the same index-probe read path without the durable-store machinery around it.
A read-only Zipfian workload (YCSB-C, a few hot keys take most of the traffic) tells the same story: tamnd/kv leads at 6,516,996 reads/sec, ahead of pogreb at 3,108,283 and buntdb at 3,096,148, and roughly 7x ahead of the fastest durable store.

These are Apple M4 numbers from a single fresh run of the whole field.
The engine ordering has held on the other machines tested before; a cross-machine re-run under the current methodology is still pending, so only the M4 numbers are published here rather than a stale multi-host table.

## When the data does not fit in cache

The table above is the cache-resident case: the working set fits in RAM, which is what a cache or read-model store is for.
Push the data past the cache and the picture changes, and it is the honest weak spot of the hash-log shape.
With 300,000 keys against a cache sized well below them, a uniform-random read has to seek into the cold tail on disk, and tamnd/kv drops to 318,000 reads/sec, below the LSM engines and bbolt, ahead of only sqlite:

| Engine | Uniform random (out of cache) | Read-latest (out of cache) |
| --- | --- | --- |
| buntdb | 3,336,600 | 40,512 |
| pogreb | 2,149,904 | 165,340 |
| bbolt | 774,270 | 999 |
| goleveldb | 726,005 | 76,382 |
| badger | 678,283 | 630,571 |
| pebble | 368,136 | 561,591 |
| **tamnd/kv** | **318,026** | **1,732,052** |
| sqlite | 8,255 | 8,255 |

The split inside that table is the whole story.
Under uniform random access, where every key is equally likely and the hot tier cannot help, tamnd/kv is mid-to-low pack, because the on-disk engines are built for exactly that and it is not.
Under skewed or read-latest access, where a hot set stays resident, it leads again at 1,732,000 reads/sec, because the hot tier keeps the working set in memory even though the whole dataset does not fit.
So the honest rule is that tamnd/kv wins reads when the hot set fits in RAM, which is the cache and read-model case it is built for, and gives up the lead when the working set is both larger than memory and uniformly accessed.

## What to pick

- **tamnd/kv** if reads are the whole job and the keyset fits in memory. Nothing here reads faster, and nothing has a tighter tail.
- **pogreb** or **buntdb** if you want the same read-first profile with a different trade: pogreb is a simpler on-disk hash-log, buntdb keeps everything in RAM.
- **goleveldb** or **pebble** if you also need ordered scans (the hash-log engines have none) and can accept reads an order of magnitude slower.

## What to avoid

- **sqlite** for pure key/value reads. It is more than 100x slower here. Its strengths are SQL and durable batching, not raw `get`.
- The hash-log engines (**tamnd/kv**, **pogreb**) if your read keyset does not fit in RAM, because the key index is in memory, or if you need [ordered scans](/scenarios/range-scans/), which they do not support.

Unlike the old transactional build of tamnd/kv, the current hash-log core is not weak on updates, so a read-heavy workload that also updates hot keys is fine here; see the [mixed scenario](/scenarios/mixed/), where it leads that mix too.
