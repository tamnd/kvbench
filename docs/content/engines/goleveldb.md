---
title: "goleveldb"
linkTitle: "goleveldb"
description: "goleveldb is a pure-Go port of LevelDB. No standout strength, no glaring weakness, and the smallest disk footprint of any engine measured."
weight: 70
---

**Shape:** LSM, pure Go, ordered
**Repository:** [github.com/syndtr/goleveldb](https://github.com/syndtr/goleveldb)

goleveldb is a pure-Go port of Google's LevelDB, the original LSM.
It is the balanced engine in this set: it never tops a table and never bottoms one, and it packs data smaller on disk than anything else here.
When you want an unsurprising LSM with no native dependencies, this is it.

## Best at

- **Smallest footprint.** 0.15x space amplification, the tightest measured, and it stays small under updates. See [footprint](/scenarios/footprint/).
- **Balance.** 1,032,000 reads/sec, 92,000 writes/sec, 167,000 keys/sec scanned: solid on every workload without a weak spot.
- **Ordered scans.** Supported and respectable, the third-fastest scanner.

## Watch out for

- **No headline strength.** Whatever the workload, another engine is faster at it; goleveldb's case is consistency, not peak speed.
- **Per-commit durability.** 1,100 durable writes/sec, no group commit.

## Reach for it when

You want a dependable, pure-Go LSM that does everything reasonably, takes the least disk, and never surprises you.
If you need the writes to scale harder on a server or the lowest mixed-load tail, [pebble](/engines/pebble/) and [badger](/engines/badger/) edge it; for plain balance and footprint, goleveldb is a fine choice.
