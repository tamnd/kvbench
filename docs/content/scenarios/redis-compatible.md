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
The measured cost of the trap is large, and it is not the same for every server.
Here is the six-core Linux box below, first with the client and server sharing all six cores, then split three-and-three, everysec, four concurrent clients:

| Engine | readrandom (shared) | readrandom (split) | ycsb-c (shared) | ycsb-c (split) |
| --- | --- | --- | --- | --- |
| redis | 29,809 | 34,055 | 24,288 | 31,258 |
| valkey | 27,987 | 35,714 | 23,547 | 30,421 |
| kv-redis | 15,612 | 26,725 | 15,794 | 26,209 |

Every server reads faster once the client is off its cores, but kv-redis gains the most by far: it nearly doubles on both reads (up 71% on readrandom, 66% on read-only), while redis and valkey pick up 14 to 29%.
The reason is the threading model.
redis and valkey are single-threaded, so they only ever want one core, and a co-located client on the other cores barely disturbs them.
kv-redis is multi-threaded and wants to spread across every core it is given, so a client sharing those cores sits on exactly the work it needs and starves it.
Co-location does not flatter the multi-threaded server, it holds it down; the split is what lets each server show its real rate on an equal core budget.
So the table below is the split run, and it is the one to trust.

## A 6-core x86-64 Linux VPS, client pinned off the server

kvbench run with `--cpu-split`, which on this box puts the server on three cores and the go-redis client on the other three, four concurrent clients, 1 KB values, everysec durability, over a unix socket.
redis 8.8.0, valkey 7.2.12, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| kv-redis | **28,376** | **27,438** | 26,725 | **27,876** | 26,209 |
| valkey | 20,972 | 17,910 | **35,714** | 27,032 | 30,421 |
| redis | 16,660 | 22,520 | 34,055 | 19,694 | **31,258** |

Read the columns, not the rows.
kv-redis leads write ingest by a clear margin, a third again the write rate of valkey and better than half again redis on fillrandom, and it leads overwrite outright.
It also edges the 50/50 ycsb-a mix, level with valkey and well ahead of redis.
redis and valkey take the pure reads: valkey the fastest on readrandom, redis on read-only ycsb-c, with kv-redis a step behind on both.
The picture is the same shape as the embedded board, where kv is strongest on writes and mixed traffic and competitive on reads, only here the network hop caps everyone at tens of thousands of ops a second instead of millions.

## Apple M4 laptop

The same servers on an Apple M4 laptop (10 cores), 8 concurrent clients, 1 KB values, everysec durability, over a unix socket.
redis 8.8.0, valkey 9.1.0, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| kv-redis | **102,992** | **100,592** | 100,228 | **99,556** | 96,889 |
| valkey | 53,808 | 56,909 | 106,185 | 66,309 | 113,267 |
| redis | 50,028 | 55,369 | **112,465** | 64,843 | **115,551** |

Take this table as indicative, not as a definitive ranking.
`--cpu-split` needs Linux and `taskset`, so on macOS the client cannot be pinned off the server, and every number here is a shared-cores run of the kind the Linux comparison just discussed.
But with ten cores and eight clients there is far more headroom than on the pinned four-core VPS, so the co-located client hurts less, and the table lands on the same ranking the isolated Linux run does: kv-redis leads writes outright, roughly doubling redis and valkey on fillrandom and overwrite, and edges the 50/50 mix, while redis and valkey take the pure reads.
Both tables agree on the shape, so this one reinforces rather than contradicts the Linux result: kv's Redis face is in the same league as redis and valkey, strongest on writes and the mix, a step behind on pure reads.

## What to take from this

- kv's Redis face is a real option, not a demo. It leads write ingest and the 50/50 mix on the isolated Linux run and is competitive on reads, at the same everysec durability, through the same client and socket.
- If you are choosing a networked RESP store, measure it on your own hardware with the client pinned off the server (`kvbench run --cpu-split ...` on Linux), because a co-located client that shares the server's cores will quietly rank the servers wrong.
- redis and valkey remain excellent, especially for reads, and they carry a far larger command surface than kv's point subset (`GET`, `SET`, `DEL`, `EXISTS`, `PING`, plus the connect handshake). kv-redis is a point key/value store on the wire, not a full Redis.

## What is not here yet

- aki has an adapter in the harness, but the build on the Linux host would not come up during this run, so it is left out rather than shown with a zero. It was in an earlier co-located table; once its server launches cleanly under the split it rejoins.
- dragonfly, garnet and kvrocks have adapters in the harness but were not installed on these hosts, so they are absent rather than estimated. Drop their server binary on `PATH` and they join the table.
- Only one Linux host is shown. It was near-idle at measurement time (load average about 0.1 on six cores), which is what a latency benchmark needs, but it is a single six-core VPS and not a broad sample of hardware. A run on a larger core count would show more of the multi-threaded server's headroom, since the split hands it only three cores here. A second and third host were meant to cross-check these numbers but were busy with real work at measurement time, so that pass is still pending.
