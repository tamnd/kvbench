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

It is the most well-rounded writer in this set: fastest fresh writes on the laptop, the lowest tail latency on a mixed read-update workload, and one of only two engines here that batch durable commits.

## Best at

- **Fresh writes on a laptop or a few cores.** 239,000 writes/sec on the M4, top of the [ingest](/scenarios/write-ingest/) table.
- **Durable writes.** 16,000 durable writes/sec through group commit, 20x the per-commit engines. See [durable writes](/scenarios/durable-writes/).
- **Low tail on a mixed load.** 215 us p99 on the 50/50 [mixed](/scenarios/mixed/) workload, the tightest there.

## Watch out for

- **Disk footprint.** 22x the raw data after a write-heavy run, the largest here, because the value log reclaims dead space lazily. It comes back as GC runs, but provision for the peak.
- **Slow ordered scans.** 29,000 keys/sec, far behind the other LSMs, because an ordered key scan keeps jumping to the value log.
- **Write speed drops on servers.** On the Linux hosts pebble overtakes it; badger's value-log GC does not scale across cores the way pebble's compaction does.

## Reach for it when

You write a lot, you need those writes durable under concurrency, and you have disk to spare.
For the same write profile with a small footprint on a server, compare [pebble](/engines/pebble/).
