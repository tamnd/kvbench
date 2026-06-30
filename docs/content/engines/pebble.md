---
title: "pebble"
linkTitle: "pebble"
description: "pebble is the pure-Go LSM that backs CockroachDB. It scales writes across cores, compresses data to a quarter of its size, and is the safest default for a server workload."
weight: 30
---

**Shape:** LSM, pure Go, ordered
**Repository:** [github.com/cockroachdb/pebble](https://github.com/cockroachdb/pebble)

pebble is the storage engine underneath CockroachDB, a mature LSM built for exactly the kind of sustained, concurrent, durable workload a database server runs.
It is rarely the single fastest engine on any one row, and it is the most consistent across all of them, which is what makes it a safe default.

Two things stand out: it is the only engine here whose write rate goes *up* on a server versus the laptop, and it compresses 1 KB values to roughly a quarter of their size on disk.

## Best at

- **Sustained writes on a server.** 120,000, 92,000, and 72,000 writes/sec on the 4, 6, and 8-core Linux hosts, fastest on every one, because its compaction scales across cores. See [ingest](/scenarios/write-ingest/).
- **Small disk footprint.** 0.26x space amplification, and it stays compact under updates. See [footprint](/scenarios/footprint/).
- **Balanced everything.** Solid reads, good ordered scans (450,000 keys/sec), controlled tail latency on mixed loads.

## Watch out for

- **Point reads trail the hash engines.** 856,000 reads/sec, good but well behind tamnd/kv and pogreb if reads are the whole job.
- **Per-commit durability.** 980 durable writes/sec; it does not group-commit like badger, so for high-rate durable writes badger is faster.

## Reach for it when

You want one engine that does everything competently on a server and never embarrasses you: writes that scale, a small footprint, ordered scans, and a predictable tail.
This is the default to pick when you are not sure which scenario you are, especially if the workload involves [mixed reads and updates](/scenarios/mixed/).
