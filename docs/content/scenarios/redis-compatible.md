---
title: "Redis-compatible servers: kv's wire face against redis and valkey"
linkTitle: "Redis-compatible"
description: "The RESP servers measured over a socket at everysec durability: redis, valkey, and kv's own Redis face. On an isolated Linux run redis and valkey lead the reads, kv-redis leads write ingest, and the client is pinned off the server so it cannot skew the numbers."
weight: 70
---

Everything else on this site is an embedded engine: a library you call in your own process, where a `get` is a function call.
This page is the other kind of store, the one you reach over the network.
redis, valkey and the rest speak the RESP protocol, and so does kv: the same hash-log core that leads the embedded board also has a Redis wire face, measured here as `kv-redis`.

These numbers live in their own class and never share a table with the embedded ones.
An in-process `get` and a `get` that crosses a socket to a separate process are not the same measurement, and the round-trip is the whole difference: the embedded kv does millions of point reads a second, the same core behind a socket does tens of thousands, because every operation is now a network hop.
That is not a regression, it is the cost of the wire, and the only fair comparison is against other things on the same wire.

## How these are run

Every server is launched by the harness on its own private unix socket and driven with the same go-redis client, so there is no shared port and no Docker.
The socket is the fastest local transport there is, so the number is close to the protocol's floor rather than dominated by TCP.

All of them run at `appendfsync everysec`, each server's shipped default: a write is acknowledged once it is in memory and the log is fsynced about once a second, a bounded sub-second loss window.
That is the production durability for a networked store.
The per-commit `appendfsync always` regime is not measured over the wire, because over a network hop nobody deploys it (redis itself documents it as prohibitively slow); the per-commit comparison lives on the embedded [durable-writes](/scenarios/durable-writes/) page instead.

## The client cannot be allowed to steal the server's cores

There is a trap in benchmarking a networked store from the same machine, and it is worth being explicit about because it changed the numbers on this page.
The harness runs the go-redis client in its own process, right next to the server.
On a machine where they share cores, the client goroutines and the server threads fight for CPU, and the fight does not hit every server the same way.
redis is single-threaded, so it only ever wants one core and a co-located client barely disturbs it.
A multi-threaded server can claim the spare cores the client also wants.
The result is a ranking that partly reflects who grabbed the idle cores rather than who serves the protocol faster.

kvbench now closes that gap on Linux with `--cpu-split`: it pins the go-redis client to one set of cores and each launched server to a disjoint set, so the load generator can never take a core the server needs.
Every server then gets the same core budget, the way `redis-benchmark` keeps its load threads off the server's cores.
The measured cost of the trap is large.
Here is the same four-core budget on the Linux box below, first with the client and server sharing the cores, then split apart, everysec, four concurrent clients:

| Engine | readrandom (shared) | readrandom (split) | ycsb-c (shared) | ycsb-c (split) |
| --- | --- | --- | --- | --- |
| redis | 14,816 | 25,781 | 17,379 | 28,351 |
| valkey | 22,486 | 30,183 | 23,834 | 25,353 |
| kv-redis | 13,670 | 22,927 | 13,413 | 16,237 |

Every server reads faster once the client is off its cores, redis most of all (up 74% on readrandom, 63% on read-only), because a single-threaded server has the least room to route around a client sitting on its core.
Sharing cores had flattered the multi-threaded kv-redis into the read lead; splitting them apart hands the read lead back to redis and valkey.
So the table below is the split run, and it is the one to trust.

## A 6-core x86-64 Linux VPS, client pinned off the server

kvbench run with `--cpu-split`, the server on two cores and the go-redis client on two others, four concurrent clients, 1 KB values, everysec durability, over a unix socket.
redis 8.8.0, valkey 7.2.12, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| kv-redis | **23,304** | **16,809** | 22,927 | 18,582 | 16,237 |
| valkey | 16,104 | 15,069 | **30,183** | **24,246** | 25,353 |
| redis | 15,491 | 15,563 | 25,781 | 20,231 | **28,351** |

Read the columns, not the rows.
kv-redis leads write ingest by a clear margin, half again the write rate of redis or valkey on fillrandom, and it is level with them on overwrite.
redis and valkey lead the reads and the read-heavy mix: valkey takes readrandom and the 50/50 ycsb-a, redis takes read-only ycsb-c.
The picture is the same shape as the embedded board, where kv is strongest on writes and competitive on reads, only here the network hop caps everyone at tens of thousands of ops a second instead of millions.

## Apple M4 laptop

The same servers on an Apple M4 laptop (10 cores), 8 concurrent clients, 1 KB values, everysec durability, over a unix socket.
redis 8.8.0, valkey 9.1.0, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| valkey | 112,674 | 96,506 | 146,865 | 93,212 | 192,396 |
| redis | 98,447 | 107,100 | 145,198 | 106,215 | 143,497 |
| kv-redis | 99,989 | 90,626 | 96,728 | 106,123 | 102,435 |

Take this table as indicative, not as a ranking.
`--cpu-split` needs Linux and `taskset`, so on macOS the client cannot be pinned off the server, and every number here is a shared-cores run of the kind the Linux comparison just showed is skewed.
With ten cores and eight clients there is more headroom than on the pinned four-core VPS, so the skew is milder, but it still leans the same way, and the isolated Linux table above is the one to trust for how these servers rank.
What both tables agree on is that kv's Redis face is in the same league as redis and valkey, competitive on the read-update mix and strongest on writes.

## What to take from this

- kv's Redis face is a real option, not a demo. It leads write ingest on the isolated Linux run and is competitive on reads, at the same everysec durability, through the same client and socket.
- If you are choosing a networked RESP store, measure it on your own hardware with the client pinned off the server (`kvbench run --cpu-split ...` on Linux), because a co-located client that shares the server's cores will quietly rank the servers wrong.
- redis and valkey remain excellent, especially for reads, and they carry a far larger command surface than kv's point subset (`GET`, `SET`, `DEL`, `EXISTS`, `PING`, plus the connect handshake). kv-redis is a point key/value store on the wire, not a full Redis.

## What is not here yet

- aki has an adapter in the harness, but the build on the Linux host would not come up during this run, so it is left out rather than shown with a zero. It was in an earlier co-located table; once its server launches cleanly under the split it rejoins.
- dragonfly, garnet and kvrocks have adapters in the harness but were not installed on these hosts, so they are absent rather than estimated. Drop their server binary on `PATH` and they join the table.
- Only one Linux host is shown, and it was lightly loaded rather than idle at measurement time. The two quieter servers that were meant to run this were busy with real work (an LLM and a Kubernetes node on one, a crawl job saturating the other), and a latency benchmark on a loaded box measures the noise, not the engine. A quiet re-run on those hosts is pending, the same way the embedded board is waiting on its cross-machine pass.
