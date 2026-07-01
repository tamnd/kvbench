---
title: "pebble"
linkTitle: "pebble"
description: "pebble is the pure-Go LSM that backs CockroachDB. It compresses data to an eighth of its size, stays strong on mixed loads, and is the safest default for a durable server workload."
weight: 30
---

**Shape:** LSM, pure Go, ordered
**Repository:** [github.com/cockroachdb/pebble](https://github.com/cockroachdb/pebble)

pebble is the storage engine underneath CockroachDB, a mature LSM built for exactly the kind of sustained, concurrent, durable workload a database server runs.
It is rarely the single fastest engine on any one row, and it is the most consistent across all of them, which is what makes it a safe default.

One thing stands out: among the durable stores it is the strongest on a read-update mix, and it compresses 1 KB values to about an eighth of their size on disk.

## Best at

- **Strongest durable engine on a mixed load.** 384,000 ops/sec on the 50/50 [mixed](/scenarios/mixed/) workload, the closest anything comes to tamnd/kv there, and it holds a controlled tail while doing it.
- **Small disk footprint.** 0.13x space amplification, second only to goleveldb, and it stays compact under updates. See [footprint](/scenarios/footprint/).
- **Balanced everything.** 713,000 reads/sec, 158,000 fresh writes/sec, and good ordered scans (367,000 keys/sec), solid on every workload without a weak spot.

## Watch out for

- **Point reads trail the hash engines.** 713,000 reads/sec, good but well behind tamnd/kv and pogreb if reads are the whole job.
- **Per-commit durability.** 383 durable writes/sec; it does not group-commit like badger, so for high-rate durable writes badger is faster.

## Reach for it when

You want one durable engine that does everything competently and never embarrasses you: a strong mixed load, a small footprint, ordered scans, and a predictable tail.
This is the default to pick among the durable stores when you are not sure which scenario you are, especially if the workload involves [mixed reads and updates](/scenarios/mixed/).
