---
title: "Smallest footprint: least disk per byte stored"
linkTitle: "Smallest footprint"
description: "When storage cost is the constraint, space amplification is the number that matters. The LSM engines compress below raw size; tamnd/kv is compact too, and badger is the cautionary tale."
weight: 60
---

This is the workload where the bill is the constraint: you have a lot of data, it lives somewhere you pay for by the gigabyte, and the question is which engine stores the same data in the least space.

The number is **space amplification**: bytes on disk divided by bytes of actual data.
1.0x is break-even.
Below 1.0x means the engine compressed your data.
Above 1.0x means overhead, old versions, or padding you are paying to keep.

These are the out-of-cache numbers, 300,000 keys of 1 KB written to disk, so every engine is measured with its data actually on the platter rather than held in memory.

## The numbers

On-disk size after writing 300,000 keys of 1 KB each, as a multiple of the raw data:

| Engine | Shape | Space amplification | Reads this as |
| --- | --- | --- | --- |
| **goleveldb** | LSM | **0.11x** | Compressed to a ninth of raw |
| **pebble** | LSM | **0.13x** | Compressed to an eighth of raw |
| **tamnd/kv** | hash-log | **0.43x** | Compressed to under half of raw |
| buntdb | in-memory B-tree | 1.03x | About break-even |
| pogreb | hash-log | 1.05x | About break-even |
| bbolt | B+tree | 2.28x | Page overhead |
| sqlite | B-tree | 4.50x | Page and index overhead |
| badger | LSM | **7.41x** | Value log not yet reclaimed |

The LSM engines that sort and compress in the background, **goleveldb** and **pebble**, store 1 KB values in a fraction of their raw size.
That is the LSM payoff: the same background work that makes writes cheap also packs the result tightly.

**tamnd/kv** is next, and comfortably compact at 0.43x, because its cold tail is compressed with zstd as it spills.
That is a change from the old transactional build of tamnd/kv, which appended a new copy and a version on every update and could balloon past twenty times the data under churn.
The hash-log core keeps a single resident pointer per key and no version history, so an update overwrites in the hot tier rather than piling up garbage, and the on-disk size stays small.

**badger** is the cautionary tale.
It is a fast writer, but it keeps values in a separate log and reclaims dead space lazily, so right after a write-heavy run it can sit at several times the data size.
That space comes back as its garbage collector runs, but if you provision disk for the steady state, provision generously.

## What to pick

- **goleveldb** or **pebble** when storage cost is the hard constraint. They store your data smaller than it arrives and stay compact under updates.
- **tamnd/kv** when you want a compact single file and the read and write speed that comes with the hash-log core, and 0.43x is small enough for the budget.
- **buntdb** if you want a predictable 1.0x and the dataset fits in RAM.

## What to avoid

- **badger** when disk is tight, unless you account for its lazy reclamation.
- **sqlite** and **bbolt** on a strict disk budget, since their page overhead puts them well above raw size.
