---
title: "Smallest footprint: least disk per byte stored"
linkTitle: "Smallest footprint"
description: "When storage cost is the constraint, space amplification is the number that matters. LSM engines compress 1 KB values below their raw size; one engine here uses 22x."
weight: 60
---

This is the workload where the bill is the constraint: you have a lot of data, it lives somewhere you pay for by the gigabyte, and the question is which engine stores the same data in the least space.

The number is **space amplification**: bytes on disk divided by bytes of actual data.
1.0x is break-even.
Below 1.0x means the engine compressed your data.
Above 1.0x means overhead, old versions, or padding you are paying to keep.

## The numbers

On-disk size after writing 100,000 keys of 1 KB each, as a multiple of the raw data:

| Engine | Shape | Space amplification | Reads this as |
| --- | --- | --- | --- |
| **goleveldb** | LSM | **0.15x** | Compressed to a seventh of raw |
| **pebble** | LSM | **0.26x** | Compressed to a quarter of raw |
| buntdb | in-memory B-tree | 1.03x | About break-even |
| pogreb | hash-log | 2.04x | Twice the data |
| bbolt | B+tree | 2.33x | Page overhead |
| sqlite | B-tree | 4.50x | Page and index overhead |
| tamnd/kv | hash-log | 4.97x | Log plus index overhead |
| badger | LSM | **22.16x** | Value log not yet reclaimed |

The LSM engines that sort and compress in the background, **goleveldb** and **pebble**, store 1 KB values in a fraction of their raw size.
That is the LSM payoff: the same background work that makes writes cheap also packs the result tightly.

**badger** is the cautionary tale.
It is one of the fastest writers, but it keeps values in a separate log and reclaims dead space lazily, so right after a write-heavy run it can sit at 22x the data size.
That space comes back as its garbage collector runs, but if you provision disk for the steady state, provision generously.

## Watch the update workloads

The fillrandom numbers above are fresh writes.
Updates change the picture, because some engines keep old copies until they compact:

| Engine | Fresh writes | Under hot-key updates |
| --- | --- | --- |
| pebble | 0.26x | 0.3x |
| goleveldb | 0.15x | 0.2x |
| buntdb | 1.0x | 1.0x |
| badger | 22x | 22x |
| **tamnd/kv** | 5.0x | **53x** |

tamnd/kv holds 5x on fresh writes but balloons to 53x under a hot-key update burst, because every update appends a new copy and the old ones wait for compaction.
If your data churns, the [mixed scenario](/scenarios/mixed/) covers this in full; the short version is that tamnd/kv is a poor fit for update-heavy storage on a disk budget.

## What to pick

- **goleveldb** or **pebble** when storage cost is the constraint. They store your data smaller than it arrives and stay compact under updates.
- **buntdb** if you want a predictable 1.0x and the dataset fits in RAM.

## What to avoid

- **badger** when disk is tight, unless you account for its lazy reclamation.
- **tamnd/kv** for update-heavy data on a disk budget, because of the 53x churn.
