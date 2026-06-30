---
title: "bbolt"
linkTitle: "bbolt"
description: "bbolt is a single-file copy-on-write B+tree, the storage under etcd. The simplest engine here, the fastest ordered scanner, and slow at random and durable writes."
weight: 40
---

**Shape:** copy-on-write B+tree, single file, pure Go
**Repository:** [github.com/etcd-io/bbolt](https://github.com/etcd-io/bbolt)

bbolt is a single-file B+tree, the storage layer under etcd and a long list of Go projects that want a dependable embedded store with no moving parts.
It is the simplest engine in this set: one file, one B+tree, a copy-on-write design that gives you a consistent snapshot for free.

Its strength is reading in order.
Because neighbouring keys sit in neighbouring pages, an ordered scan is close to a sequential disk read, and bbolt scans faster than anything else here.

## Best at

- **Ordered scans.** 766,000 keys/sec, the fastest scanner measured. See [range scans](/scenarios/range-scans/).
- **Read-mostly simplicity.** 865,000 random reads/sec from a single file with zero configuration.
- **Consistent snapshots.** The copy-on-write design means a read transaction sees a stable view while writes continue.

## Watch out for

- **Slow random writes.** 38,000 writes/sec, near the bottom, because a copy-on-write B+tree copies a path of pages on each commit.
- **Slowest durable writes.** 110 per second with a flush on every commit, the floor in this set.
- **Space.** 2.3x the data, the page-level overhead of a B+tree.

## Reach for it when

You want a rock-solid single-file store for a read-mostly or scan-heavy workload and your write rate is modest.
It is the boring, dependable choice, and boring is often correct.
If you need heavy writes alongside the scans, look at [pebble](/engines/pebble/) instead.
