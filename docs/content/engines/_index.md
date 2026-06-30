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

Throughput is operations per second on the Apple M4, 100,000 keys of 1 KB, 8 concurrent clients, disk flush off.
Space is on-disk bytes per byte of data after fresh writes.

| Engine | Shape | Random reads | Fresh writes | Ordered scan | Space | Durable writes |
| --- | --- | --- | --- | --- | --- | --- |
| [tamnd/kv](/engines/tamnd-kv/) | hash-log | 6,848,000 | 83,000 | no | 5.0x | 740 |
| [badger](/engines/badger/) | LSM | 594,000 | 239,000 | yes | 22x | 16,000 |
| [pebble](/engines/pebble/) | LSM | 856,000 | 97,000 | yes | 0.26x | 980 |
| [bbolt](/engines/bbolt/) | B+tree | 865,000 | 38,000 | yes | 2.3x | 110 |
| [buntdb](/engines/buntdb/) | in-memory B-tree | 3,572,000 | 230,000 | yes | 1.0x | 250 |
| [pogreb](/engines/pogreb/) | hash-log | 4,008,000 | 190,000 | no | 2.0x | 360 |
| [goleveldb](/engines/goleveldb/) | LSM | 1,032,000 | 92,000 | yes | 0.15x | 1,100 |
| [sqlite](/engines/sqlite/) | B-tree | 45,000 | 29,000 | yes | 4.5x | 17,000 |

No engine wins every column.
The hash-logs own reads and lose on scans and update churn; the LSMs own writes and disk footprint; the B-trees own ordered scans and durable batching.
Pick by the column your workload lives in, then read that engine's page.

## What "shape" means

- **hash-log** appends every write to one file with an in-memory index. Fastest point reads, no ordered scan, update churn.
- **LSM** buffers writes and merges them in the background. Cheap writes, small on disk, occasional merge latency.
- **B+tree** keeps keys sorted in pages. Great ordered scans, slower random writes.

The [start-here pages](/start/what-is-a-key-value-store/) explain the shapes in full.
