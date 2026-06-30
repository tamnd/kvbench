---
title: "Scenarios"
linkTitle: "Scenarios"
description: "Pick a key/value engine by what your workload does most: read-heavy, write-ingest, mixed read-update, durable writes, ordered scans, or smallest footprint. Each scenario has real numbers."
weight: 20
featured: true
---

The fastest way to pick an engine is to name what your workload does most, then read the one scenario that matches.
Each page below states the question, names the winners with real numbers from four machines, and says plainly what to avoid and why.

All throughput figures are operations per second.
Unless a table says otherwise, the headline number is an Apple M4 laptop (10 cores, 24 GB), 100,000 keys of 1 KB each, 8 concurrent clients.
Where an engine scales differently on a server, the page shows the cross-machine numbers too.

| Scenario | The question | Top picks |
| --- | --- | --- |
| [Read-heavy](/scenarios/read-heavy/) | Mostly reading keys you already wrote | tamnd/kv, pogreb, buntdb |
| [Write ingest](/scenarios/write-ingest/) | A firehose of new keys | pebble, badger, buntdb |
| [Mixed read-update](/scenarios/mixed/) | Reads and updates in roughly equal measure | buntdb, badger, pebble |
| [Durable writes](/scenarios/durable-writes/) | Every write must survive a crash | badger, sqlite |
| [Range scans](/scenarios/range-scans/) | Walking keys in sorted order | bbolt, pebble, goleveldb |
| [Smallest footprint](/scenarios/footprint/) | Least disk per byte stored | goleveldb, pebble |

If you are not sure which one you are, you are probably [mixed read-update](/scenarios/mixed/): most real services read and write in roughly equal measure.
Start there.
