---
title: "Engines"
linkTitle: "Engines"
description: "The eight embedded Go key/value engines kvbench measures, one page each: what it is, what it is best at with real numbers, and what to watch out for."
weight: 30
featured: true
---

Eight embedded key/value engines, all pure Go, all added with `go get`, all measured through the identical harness.
Each page below is a short card: the shape, the best workload with real numbers, the weak spot, and when to reach for it.

## At a glance

Random reads are cache-resident (100,000 keys of 1 KB); fresh writes and space are out-of-cache (300,000 keys written to disk); durable writes are the FULL regime with a flush on every commit.
All on the Apple M4, 8 concurrent clients.
Each column names its own setup because a read that fits in cache and a write that spills to disk are different questions.

| Engine | Shape | Random reads | Fresh writes | Ordered scan | Space | Durable writes |
| --- | --- | --- | --- | --- | --- | --- |
| [tamnd/kv](/engines/tamnd-kv/) | hash-log | 6,955,000 | 5,711,000 | no | 0.43x | 224 |
| [badger](/engines/badger/) | LSM | 561,000 | 320,000 | yes | 7.41x | 4,676 |
| [pebble](/engines/pebble/) | LSM | 713,000 | 158,000 | yes | 0.13x | 383 |
| [bbolt](/engines/bbolt/) | B+tree | 698,000 | 52 | yes | 2.28x | 52 |
| [buntdb](/engines/buntdb/) | in-memory B-tree | 3,236,000 | 16,000 | yes | 1.03x | 99 |
| [pogreb](/engines/pogreb/) | hash-log | 1,748,000 | 17,000 | no | 1.05x | 158 |
| [goleveldb](/engines/goleveldb/) | LSM | 815,000 | 28,000 | yes | 0.11x | 463 |
| [sqlite](/engines/sqlite/) | B-tree | 52,000 | 7,500 | yes | 4.50x | 2,152 |

No engine wins every column.
tamnd/kv owns reads and background-flush writes and stays compact on disk; the LSMs own disk footprint and durable batching; the B-trees own ordered scans; badger and sqlite own per-commit durable throughput through group commit.
Pick by the column your workload lives in, then read that engine's page.

## What "shape" means

- **hash-log** appends every write to one file with an in-memory index. Fastest point reads, no ordered scan, update churn.
- **LSM** buffers writes and merges them in the background. Cheap writes, small on disk, occasional merge latency.
- **B+tree** keeps keys sorted in pages. Great ordered scans, slower random writes.

The [start-here pages](/start/what-is-a-key-value-store/) explain the shapes in full.
