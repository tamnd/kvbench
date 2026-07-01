---
title: "Read-heavy: mostly reading keys you already wrote"
linkTitle: "Read-heavy"
description: "Caches, lookup tables, and read-mostly indexes. tamnd/kv reads at over 8 million keys per second, ahead of every other embedded Go engine measured."
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
| **tamnd/kv** | hash-log | **8,269,418** | 6 us |
| pogreb | hash-log | 3,867,593 | 25 us |
| buntdb | in-memory B-tree | 3,291,885 | 25 us |
| bbolt | B+tree | 1,030,319 | 212 us |
| goleveldb | LSM | 902,799 | 210 us |
| pebble | LSM | 830,670 | 225 us |
| badger | LSM | 803,045 | 395 us |
| sqlite | B-tree | 46,171 | 1.7 ms |

tamnd/kv reads faster than everything else here, with the tightest tail by a wide margin: a 6 microsecond p99 against 210 microseconds for the first LSM.
That gap is the hash-log shape doing what it is built for.

The size of the lead depends on what you compare against, and the honest split matters.
Against the durable on-disk stores, badger, pebble, goleveldb, bbolt and sqlite, tamnd/kv reads 8x faster than the quickest of them.
Against the two read-first neighbours, the in-memory buntdb and the on-disk hash-log pogreb, the lead is about two times, not eight, because they share the same index-probe read path without the durable-store machinery around it.
A read-only Zipfian workload (YCSB-C, a few hot keys take most of the traffic) tells the same story: tamnd/kv leads at 7,843,691 reads/sec, ahead of buntdb at 3,910,562 and pogreb at 3,831,273, and roughly 7x ahead of the fastest durable store.

These are Apple M4 numbers from a single fresh run of the whole field.
The engine ordering has held on the other machines tested before; a cross-machine re-run under the current methodology is still pending, so only the M4 numbers are published here rather than a stale multi-host table.

## When the data does not fit in cache

The table above is the cache-resident case: the working set fits in RAM, which is what a cache or read-model store is for.
Push the data past the cache and the picture changes, and it is the honest weak spot of the hash-log shape.
With 300,000 keys against a cache sized well below them, a uniform-random read has to seek into the cold tail on disk, and tamnd/kv drops to 405,000 reads/sec, below the LSM engines and bbolt, ahead of only sqlite:

| Engine | Uniform random (out of cache) | Read-latest (out of cache) |
| --- | --- | --- |
| buntdb | 3,839,699 | 461,835 |
| pogreb | 3,516,981 | 915,790 |
| bbolt | 1,073,757 | 2,945 |
| badger | 638,680 | 678,327 |
| goleveldb | 617,596 | 343,506 |
| pebble | 589,377 | 533,476 |
| **tamnd/kv** | **404,851** | 551,554 |
| sqlite | 4,042 | 1,985 |

The split inside that table is the whole story.
Under uniform random access, where every key is equally likely and the hot tier cannot help, tamnd/kv is mid-to-low pack, because the on-disk engines are built for exactly that and it is not.
Under read-latest access, where a recent hot set stays resident, it recovers to 552,000, ahead of its own uniform rate and of pebble and goleveldb, because the hot tier keeps that working set in memory even though the whole dataset does not fit; but pogreb and badger lead this column, so the hot tier narrows the gap rather than restoring the outright lead it holds in cache.
So the honest rule is that tamnd/kv wins reads outright when the whole keyset fits in RAM, which is the cache and read-model case it is built for, and once the dataset spills past memory it is mid-pack, best under skew and weakest under uniform access.

## What to pick

- **tamnd/kv** if reads are the whole job and the keyset fits in memory. Nothing here reads faster, and nothing has a tighter tail.
- **pogreb** or **buntdb** if you want the same read-first profile with a different trade: pogreb is a simpler on-disk hash-log, buntdb keeps everything in RAM.
- **goleveldb** or **pebble** if you also need ordered scans (the hash-log engines have none) and can accept reads an order of magnitude slower.

## What to avoid

- **sqlite** for pure key/value reads. It is more than 100x slower here. Its strengths are SQL and durable batching, not raw `get`.
- The hash-log engines (**tamnd/kv**, **pogreb**) if your read keyset does not fit in RAM, because the key index is in memory, or if you need [ordered scans](/scenarios/range-scans/), which they do not support.

Unlike the old transactional build of tamnd/kv, the current hash-log core is not weak on updates, so a read-heavy workload that also updates hot keys is fine here; see the [mixed scenario](/scenarios/mixed/), where it leads that mix too.
