---
title: "tamnd/kv"
linkTitle: "tamnd/kv"
description: "tamnd/kv is a single-file embedded store with a hash-log core and full transactions. It reads faster than any engine measured, and is weak on update-heavy workloads."
weight: 10
---

**Shape:** hash-log (Bitcask-style), single file, MVCC transactions
**Repository:** [github.com/tamnd/kv](https://github.com/tamnd/kv)

tamnd/kv is the engine this benchmark was built to keep honest, and it runs through the same adapter as every other store with no special path.
It is a single-file embedded database with a hash-log core (a latch-free sharded hash index over an append log) and a full transactional shell on top: WAL, MVCC, and serializable transactions.

The headline is reads.
A `get` is one in-memory index probe and one seek, and at 6,848,000 random reads per second on the Apple M4 it is the fastest engine measured here, with a 6 microsecond p99, the tightest tail by a wide margin.
That lead holds on every machine tested.

## Best at

- **Point reads.** Fastest here, on every host, by roughly 1.7x over the next engine. See [read-heavy](/scenarios/read-heavy/).
- **Read-mostly workloads.** A read-only Zipfian mix runs at 5,379,000 ops/sec.
- **Single-file deployment.** The whole store is one file, easy to copy, back up, or ship.

## Watch out for

- **No ordered scan.** The hash-log stores keys in write order. Prefix and range queries are not supported. Rules it out for [scan workloads](/scenarios/range-scans/).
- **Hot-key updates.** Under a 50/50 read-update mix it drops to 2,900 ops/sec and its disk grows to 53x the data, because every update appends a new copy and a transaction version that wait for compaction. This is its worst case; see [mixed](/scenarios/mixed/).
- **In-memory index.** The key index lives in RAM and scales with the number of keys, so a very large keyset needs memory to match.
- **Durable write rate.** With a flush on every commit it does 740 writes/sec, mid-pack, because it does not group-commit like badger or sqlite.

## Reach for it when

Reads dominate, the keyset fits in memory, and you do not need ordered scans.
That is the cache, the lookup table, the read-model store.
For update-heavy or scan-heavy work, pick a different shape from this list.

## A note on durability defaults

tamnd/kv ships a default that flushes on a short timer and at checkpoints rather than on every commit: no corruption on a crash, a sub-second worst-case loss window, and far better throughput than a per-commit flush.
The strict per-commit mode is one option away when you need it, and that strict mode is what the durable-writes numbers on this site measure, so every engine is compared on the same footing.
