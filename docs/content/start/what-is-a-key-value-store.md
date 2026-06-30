---
title: "What a key/value store is"
linkTitle: "What it is"
description: "A key/value store is a database that maps a key to a value. The three internal shapes (B-tree, LSM, hash-log) decide what it is fast and slow at."
weight: 10
---

A key/value store maps a key to a value.
You hand it `user:42` and a blob of bytes, it keeps them together, and later you ask for `user:42` and get the bytes back.
That is the whole interface: `put`, `get`, `delete`, and on most engines a `scan` that walks keys in order.

An **embedded** store is a library that runs inside your program and keeps its data in a file on the same machine.
There is no server to start, no port, no network hop.
Every engine on this site is embedded and written in Go, so you add it with `go get` and it runs anywhere Go runs.
That is the kind of store you reach for when you want a local cache, an index, a queue, an offline dataset, or the storage layer under your own service.

## Why they are not all the same

Every engine does `put` and `get`, so on a casual test they all look fast.
The differences only show up under load, and they come from one decision: how the engine lays its data out on disk.
There are three common shapes, and almost every store is one of them.

### B-tree: keys sorted in a tree

A B-tree keeps every key in sorted order in a tree of pages, the same structure most SQL databases use.
Reads are quick because finding a key is walking down a few levels.
Ordered scans are excellent because neighbouring keys sit next to each other.
The cost is writes: inserting a key can mean rewriting a page, and on a crash-safe B-tree, copying a whole path of pages on every commit.

On this site **bbolt** and **sqlite** are B-trees, and **buntdb** is a B-tree that keeps everything in memory.

### LSM: writes buffered, then merged

A log-structured merge tree (LSM) takes the opposite trade.
A write lands in an in-memory buffer plus a small append to a log, and that is it: no tree to rewrite.
The buffers are flushed and merged into sorted files in the background.
This makes writes very cheap and keeps the on-disk format compressible, often smaller than the raw data.
The cost is read amplification (a key might live in any of several files) and background merge work that can show up as a latency spike.

On this site **pebble**, **badger**, and **goleveldb** are LSMs.

### Hash-log: append everything, index in memory

A hash-log (the Bitcask design) appends every write to the end of one file and keeps an in-memory index from key to file position.
A read is one index lookup and one disk seek, so point reads are the fastest of any shape.
The costs are real: the index lives in RAM so it scales with the number of keys, there is no ordered scan because the file is in write order, and updating the same key repeatedly leaves old copies behind that have to be compacted away later.

On this site **tamnd/kv** and **pogreb** are hash-logs.

## The one-line summary

| Shape | Fast at | Slow at | Here |
| --- | --- | --- | --- |
| B-tree | Ordered scans, balanced reads | Random writes | bbolt, sqlite, buntdb |
| LSM | Writes, small on disk | Read amplification, merge spikes | pebble, badger, goleveldb |
| Hash-log | Point reads | No scans, update churn | tamnd/kv, pogreb |

Knowing the shape tells you most of what an engine will be good and bad at before you run anything.
The [scenarios](/scenarios/) put real numbers on it.
Next, [how to read those numbers](/start/reading-the-numbers/).
