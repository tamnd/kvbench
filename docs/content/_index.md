---
title: "kvbench"
description: "A neutral benchmark for embedded key/value stores. Pick a store on real numbers, not folklore."
heroTitle: "Pick a key/value store on real numbers"
heroLead: "kvbench runs eight embedded Go key/value engines through one identical harness and reports what they actually do: how fast they read, how fast they write, how much disk they use, and how they behave when you ask for durability. No home-field engine, no cherry-picked cells."
heroPrimaryURL: "/start/"
heroPrimaryText: "New to key/value? Start here"
heroSecondaryURL: "/scenarios/"
heroSecondaryText: "Pick by use case"
---

A key/value store is the simplest database there is: put a value under a key, get it back later.
Almost every embedded one is fast enough until your workload finds the thing it is bad at.
kvbench exists to find that thing before production does.

Every engine here runs through the same loader, the same clock, and the same latency histogram, so the only difference between two numbers is the engine.
The numbers on this site come from four machines: an Apple M4 laptop and three Linux servers (4, 6, and 8 cores).
Unless a table says otherwise, the headline figure is the Apple M4.

## Which engine should I use?

Start from what your workload does most, then read the scenario page for the real numbers.

| If your workload is mostly... | Reach for | Why |
| --- | --- | --- |
| [Reading keys you already wrote](/scenarios/read-heavy/) | **tamnd/kv**, pogreb, buntdb | Point reads at 4-7 million per second |
| [Writing a firehose of new keys](/scenarios/write-ingest/) | **pebble**, badger, buntdb | LSM engines absorb writes without rewriting a tree |
| [A 50/50 read-update mix](/scenarios/mixed/) | **buntdb**, badger, pebble | They keep updates cheap as the dataset churns |
| [Durable writes that survive a crash](/scenarios/durable-writes/) | **badger**, **sqlite** | They batch many commits into one disk flush |
| [Scanning keys in order](/scenarios/range-scans/) | **bbolt**, pebble, goleveldb | B-trees and LSMs keep keys sorted on disk |
| [Smallest disk footprint](/scenarios/footprint/) | **goleveldb**, pebble | LSM compression packs 1 KB values below their raw size |

These are starting points, not verdicts.
Every engine wins somewhere and loses somewhere, and the [scenarios](/scenarios/) show exactly where.

## The eight engines, in one line each

| Engine | Shape | Best at | Watch out for |
| --- | --- | --- | --- |
| [tamnd/kv](/engines/tamnd-kv/) | hash-log | Point reads (fastest measured) | No ordered scan, hot-key updates churn |
| [badger](/engines/badger/) | LSM | Writes plus durable batching | Uses 22x the raw data on disk |
| [pebble](/engines/pebble/) | LSM | Writes that scale across cores, tiny on disk | Point reads trail the hash engines |
| [bbolt](/engines/bbolt/) | B+tree | Ordered scans, dead-simple file | Random writes are slow |
| [buntdb](/engines/buntdb/) | in-memory B-tree | Fast at everything that fits in RAM | The whole dataset lives in memory |
| [pogreb](/engines/pogreb/) | hash-log | Steady point reads | No ordered scan |
| [goleveldb](/engines/goleveldb/) | LSM | Balanced, smallest disk footprint | No standout strength |
| [sqlite](/engines/sqlite/) | B-tree | Real SQL, durable batching | Slowest on raw key/value ops |

Full pros and cons per engine are on the [engines](/engines/) page.

## How to read the numbers

Three things decide which engine fits, and this site reports all three:

- **Throughput** is operations per second, sustained over the whole measured window, not a warm-up burst.
- **Tail latency** is the p99: 99 out of 100 operations finish faster than this. A good average with a bad p99 means occasional long stalls.
- **Durability** is whether a write survives a crash. We run every engine two ways: with the disk flush off (raw speed) and with a flush on every commit (the real cost of durability). The two are never mixed in one table.

If any of those terms are new, the [start here](/start/) page explains them in plain language before you read a single table.

## Why trust these numbers

The harness core never imports a concrete engine.
Every store sits behind one adapter interface, so the workload driver, the clock, and the latency histogram cannot tell which engine they are hitting.
When two engines are compared, the only thing that changes is the engine.
The full method, the machines, and the fairness rules are on the [methodology](/methodology/) page, and the [code is on GitHub](https://github.com/tamnd/kvbench) so you can run it yourself.
