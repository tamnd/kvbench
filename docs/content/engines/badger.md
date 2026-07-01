---
title: "badger"
linkTitle: "badger"
description: "badger is a pure-Go LSM key/value store with a separate value log. Fast writes, the lowest tail on mixed workloads, group-commit durability, and a large disk footprint."
weight: 20
---

**Shape:** LSM with separate value log, pure Go, transactions
**Repository:** [github.com/dgraph-io/badger](https://github.com/dgraph-io/badger)

badger is an LSM that stores keys and values separately: keys go in the LSM tree, values in a companion log.
That split makes writes cheap and keeps the tree small, at the cost of disk space and slower ordered scans.

It is the best durable writer in this set: the fastest LSM on fresh writes, and one of only two engines here that batch durable commits, which makes it the winner when every write must hit the disk.

## Best at

- **Durable writes.** 4,676 durable writes/sec through group commit, an order of magnitude past the per-commit engines and the top of the [durable-writes](/scenarios/durable-writes/) table. This is badger's headline.
- **Fresh LSM writes.** 320,000 writes/sec out of cache, the fastest LSM on the [ingest](/scenarios/write-ingest/) table, behind only tamnd/kv's hot-tier design.
- **Low tail on a mixed load.** 203 us p99 on the 50/50 [mixed](/scenarios/mixed/) workload, the tightest among the durable stores.

## Watch out for

- **Disk footprint.** 7.4x the raw data after a fresh write-heavy run, the largest here, and it climbs higher under update churn, because the value log reclaims dead space lazily. It comes back as GC runs, but provision for the peak.
- **Slow ordered scans.** 22,000 keys/sec, far behind the other LSMs, because an ordered key scan keeps jumping to the value log.
- **Point reads trail the hash engines.** 561,000 reads/sec, fine for an LSM but far behind tamnd/kv and pogreb on pure [reads](/scenarios/read-heavy/).

## Reach for it when

Every write must be durable under concurrency and you have disk to spare.
For the fastest writes when a bounded sub-second loss window is acceptable, compare [tamnd/kv](/engines/tamnd-kv/); for a small on-disk footprint, compare [pebble](/engines/pebble/).
