---
title: "Read-heavy: mostly reading keys you already wrote"
linkTitle: "Read-heavy"
description: "Caches, lookup tables, and read-mostly indexes. tamnd/kv reads at 6.8 million keys per second, ahead of every other embedded Go engine measured."
weight: 10
---

This is the cache and lookup-table workload: you write the data once (or rarely), and after that the job is almost entirely `get`.
Session stores, feature flags, denormalised read models, anything that sits in front of a slower system of record.

When reads dominate, the engine's storage shape barely matters except for one thing: how few steps a `get` takes.
The hash-log engines win here because a read is one in-memory index lookup and one disk seek, nothing more.

## The numbers

Pure random reads, 100,000 keys, 1 KB values, 8 concurrent clients, on the Apple M4:

| Engine | Shape | Reads/sec | p99 latency |
| --- | --- | --- | --- |
| **tamnd/kv** | hash-log | **6,848,000** | 6 us |
| pogreb | hash-log | 4,008,000 | 16 us |
| buntdb | in-memory B-tree | 3,572,000 | 15 us |
| goleveldb | LSM | 1,032,000 | 129 us |
| bbolt | B+tree | 865,000 | 233 us |
| pebble | LSM | 856,000 | 222 us |
| badger | LSM | 594,000 | 442 us |
| sqlite | B-tree | 45,000 | 1.8 ms |

tamnd/kv reads roughly 1.7x faster than the next engine and 150x faster than sqlite, with the tightest tail by a wide margin: a 6 microsecond p99 against 129 microseconds for the first LSM.
That gap is the hash-log shape doing what it is built for.

The ordering holds on every machine, only the absolute rate drops with fewer or slower cores:

| Engine | M4 | EPYC 4-core | EPYC 6-core | EPYC 8-core |
| --- | --- | --- | --- | --- |
| **tamnd/kv** | 6,848,000 | 1,670,000 | 1,013,000 | 978,000 |
| pogreb | 4,008,000 | 1,121,000 | 816,000 | 544,000 |
| buntdb | 3,572,000 | 1,229,000 | 833,000 | 712,000 |
| goleveldb | 1,032,000 | 406,000 | 240,000 | 188,000 |

A read-only workload (YCSB-C, Zipfian-skewed so a few hot keys take most of the traffic) tells the same story: tamnd/kv leads at 5,379,000 reads/sec, ahead of buntdb at 3,994,000 and pogreb at 3,826,000.

## What to pick

- **tamnd/kv** if reads are the whole job and the keyset fits in memory. Nothing here reads faster.
- **pogreb** or **buntdb** if you want the same read-first profile with a different trade: pogreb is a simpler on-disk hash-log, buntdb keeps everything in RAM and is strong on writes too.
- **goleveldb** if you also need ordered scans (the hash-log engines have none) and can accept reads an order of magnitude slower.

## What to avoid

- **sqlite** for pure key/value reads. It is 150x slower here. Its strengths are SQL and durable batching, not raw `get`.
- The hash-log engines (**tamnd/kv**, **pogreb**) if your read keyset does not fit in RAM, because the index is in memory, or if you need [ordered scans](/scenarios/range-scans/), which they do not support.

One caveat that does not show in a read-only table: if this workload also does frequent updates to the same hot keys, read the [mixed scenario](/scenarios/mixed/) before committing to tamnd/kv, because update churn is its weak spot.
