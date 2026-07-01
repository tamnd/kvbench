---
title: "Range scans: walking keys in sorted order"
linkTitle: "Range scans"
description: "When you need to iterate keys in order (prefix scans, range queries, ordered exports), you need an engine that keeps keys sorted. bbolt, pebble, and goleveldb do."
weight: 50
---

This is the workload where order matters: you want all keys with a prefix, or every key between two bounds, or the whole dataset walked in sorted order for an export or a migration.

Not every engine can do this at all.
The hash-log engines store keys in write order, so there is no such thing as "the next key in order" for them.
If you need scans, that rules out two of the eight engines before you look at a single number.

## Can the engine scan in order?

| Engine | Ordered scan? |
| --- | --- |
| bbolt | Yes |
| pebble | Yes |
| goleveldb | Yes |
| buntdb | Yes |
| badger | Yes |
| sqlite | Yes |
| **tamnd/kv** | **No** |
| **pogreb** | **No** |

tamnd/kv and pogreb are hash-logs: fast point reads, no ordering.
If your workload needs scans, stop here for those two.

## The numbers

Sequential scan throughput, walking keys in order, 100,000 keys, 1 KB values, on the Apple M4:

| Engine | Shape | Keys scanned/sec | p99 |
| --- | --- | --- | --- |
| **bbolt** | B+tree | **707,000** | 234 us |
| pebble | LSM | 367,000 | 436 us |
| goleveldb | LSM | 156,000 | 459 us |
| buntdb | in-memory B-tree | 98,000 | 1.7 ms |
| badger | LSM | 22,000 | 3.6 ms |
| sqlite | B-tree | 13,000 | 6.7 ms |

bbolt wins scans cleanly, and this is its best workload.
A B+tree stores neighbouring keys in neighbouring pages, so a scan is close to a sequential disk read.
pebble is the strong LSM alternative when you also need fast [ingest](/scenarios/write-ingest/), which bbolt does not have.

badger scans far slower than the other LSMs here because its values live in a separate log, so an ordered scan over keys keeps jumping out to fetch values.

## What to pick

- **bbolt** when ordered iteration is the main job and writes are modest. It is the simplest engine here (a single file, one B+tree) and the fastest scanner.
- **pebble** or **goleveldb** when you need ordered scans *and* a heavy write rate. They give up some scan speed for an LSM's write path and a tiny disk footprint.
- **buntdb** if the dataset fits in RAM and you want ordered scans alongside fast point reads.

## What to avoid

- **tamnd/kv** and **pogreb** entirely: they cannot scan in order. If you find yourself wanting a prefix scan on a hash-log, you have picked the wrong shape.
- **badger** for scan-heavy work, because its separate value log makes ordered scans the slowest of the engines that support them.
