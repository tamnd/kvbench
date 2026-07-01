---
title: "pogreb"
linkTitle: "pogreb"
description: "pogreb is a pure-Go hash-log built for fast point reads on datasets larger than memory. Like tamnd/kv it has no ordered scan, and it is a simpler bare-bitcask design."
weight: 60
---

**Shape:** hash-log (Bitcask-style), pure Go
**Repository:** [github.com/akrylysov/pogreb](https://github.com/akrylysov/pogreb)

pogreb is a hash-log key/value store designed for fast random reads on datasets too big to keep in memory.
It is the same family as tamnd/kv, but the plain Bitcask design: an append-only log with an in-memory index, no hot tier in front of it, no transactions.
That makes it a good reference point for the hash-log shape stripped to its essentials.

## Best at

- **Fast point reads.** 1,748,000 reads/sec cache-resident, third on the board behind tamnd/kv and buntdb. See [read-heavy](/scenarios/read-heavy/).
- **Reads on a working set larger than memory.** Out of cache it holds 2,149,000 uniform random reads/sec, near the top of the field, because a read is one index probe and one seek. See [read-heavy](/scenarios/read-heavy/).
- **Steady writes.** 17,000 fresh writes/sec out of cache, mid-pack.

## Watch out for

- **No ordered scan.** Same as every hash-log: keys are in write order, no range queries. See [range scans](/scenarios/range-scans/).
- **Update churn.** The plain design has no hot tier, so repeatedly updating the same key leaves old copies in the log to be compacted later. On the 50/50 [mixed](/scenarios/mixed/) workload it holds 38,000 ops/sec, well behind tamnd/kv's hot-tier design.
- **Durable write rate.** 158 per second with a flush on every commit. See [durable writes](/scenarios/durable-writes/).
- **No transactions.** It is a plain key/value store; if you need atomic multi-key commits, look elsewhere.

## Reach for it when

You want a simple hash-log for a read-mostly dataset larger than memory, and you do not need transactions, ordered scans, or a heavy update mix.
If reads are truly the entire workload and the keyset fits in RAM, [tamnd/kv](/engines/tamnd-kv/) reads faster and its hot tier absorbs updates that would churn pogreb's log.
