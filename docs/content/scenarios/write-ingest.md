---
title: "Write ingest: a firehose of new keys"
linkTitle: "Write ingest"
description: "Event logs, metrics, and bulk loads that write new keys fast. LSM engines like pebble and badger absorb writes without rewriting a tree."
weight: 20
---

This is the ingest workload: you are writing a stream of new keys as fast as they arrive.
Event logs, metrics pipelines, crawl output, bulk imports.
Reads happen later or elsewhere; right now the only thing that matters is keeping up with the write rate.

Writing new keys favours engines that do not rewrite a tree on every insert.
That is the LSM trade: a write is an in-memory buffer insert plus a small log append, and the sorting happens later in the background.

These numbers are with the disk flush off, so they measure the engine's structural write speed, not the disk.
If every write must survive a crash, that is a different question with different winners, on the [durable-writes page](/scenarios/durable-writes/).

## The numbers

Writing 100,000 fresh random keys, 1 KB values, 8 concurrent clients:

| Engine | Shape | M4 | EPYC 4-core | EPYC 6-core | EPYC 8-core |
| --- | --- | --- | --- | --- | --- |
| badger | LSM | **239,000** | 61,000 | 22,000 | 32,000 |
| buntdb | in-memory B-tree | 230,000 | 85,000 | 44,000 | 63,000 |
| pogreb | hash-log | 190,000 | 84,000 | 50,000 | 54,000 |
| pebble | LSM | 97,000 | **120,000** | **92,000** | **72,000** |
| goleveldb | LSM | 92,000 | 54,000 | 30,000 | 37,000 |
| tamnd/kv | hash-log | 83,000 | 25,000 | 12,000 | 17,000 |
| bbolt | B+tree | 38,000 | 20,000 | 11,000 | 10,000 |
| sqlite | B-tree | 29,000 | 8,000 | 3,000 | 5,000 |

There is a twist here worth noticing.
badger is fastest on the M4 laptop, but **pebble is fastest on every Linux server** and barely slows as the data grows, because its compaction scales across cores where badger's value-log GC does not.
If your ingest runs on a server, pebble is the safer bet; on a laptop or a few cores, badger edges it.

bbolt and sqlite sit at the bottom because a B-tree insert can rewrite a page, the exact cost the LSM shape avoids.

## What to pick

- **pebble** for sustained ingest on a server. It is the most consistent writer across machines and compresses the result smallest on disk (see [footprint](/scenarios/footprint/)).
- **badger** for ingest on a laptop or a few cores, where its in-memory write path is fastest, as long as you can afford its disk footprint (it uses 22x the raw data until its background GC catches up).
- **buntdb** if the dataset fits in RAM and you want fast writes and fast reads from one engine.

## What to avoid

- **bbolt** and **sqlite** for write-heavy ingest. The B-tree page rewrite caps them well below the LSM engines.
- **badger** if disk space is tight. Its write speed comes with the highest space amplification measured here.
- **tamnd/kv** if ingest is the main job. It is mid-pack on fresh writes and its strength is on the [read side](/scenarios/read-heavy/), not ingest.
