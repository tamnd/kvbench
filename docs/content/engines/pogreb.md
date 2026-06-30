---
title: "pogreb"
linkTitle: "pogreb"
description: "pogreb is a pure-Go hash-log built for fast point reads on datasets larger than memory. Like tamnd/kv it has no ordered scan, but it holds up better under updates."
weight: 60
---

**Shape:** hash-log (Bitcask-style), pure Go
**Repository:** [github.com/akrylysov/pogreb](https://github.com/akrylysov/pogreb)

pogreb is a hash-log key/value store designed for fast random reads on datasets too big to keep in memory.
It is the same family as tamnd/kv, simpler in scope: no transactions, no MVCC, just a fast append-and-index store.
That simplicity shows up as steadier behaviour under updates than the full transactional hash-log.

## Best at

- **Fast point reads.** 4,008,000 reads/sec, second only to tamnd/kv. See [read-heavy](/scenarios/read-heavy/).
- **Read-mostly with some updates.** It holds 304,000 ops/sec on the 50/50 [mixed](/scenarios/mixed/) workload at 1.5x space, where the transactional hash-log collapses.
- **Steady writes.** 190,000 fresh writes/sec, mid-to-upper pack.

## Watch out for

- **No ordered scan.** Same as every hash-log: keys are in write order, no range queries. See [range scans](/scenarios/range-scans/).
- **Durable write rate.** 360 per second with a flush on every commit.
- **No transactions.** It is a plain key/value store; if you need atomic multi-key commits, look elsewhere.

## Reach for it when

You want tamnd/kv's read-first profile but with a dataset larger than memory and some updates in the mix, and you do not need transactions or scans.
If reads are truly the entire workload and the keyset fits in RAM, [tamnd/kv](/engines/tamnd-kv/) reads faster.
