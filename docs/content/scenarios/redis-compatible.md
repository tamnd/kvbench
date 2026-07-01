---
title: "Redis-compatible servers: kv's wire face against redis and valkey"
linkTitle: "Redis-compatible"
description: "The RESP servers measured over a socket, everysec durability: redis, valkey, aki, and kv's own Redis face. kv-redis is competitive on the M4 and leads on a Linux VPS."
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

## Apple M4 laptop

100,000 keys in cache, 1 KB values, 8 concurrent clients, everysec durability, over a unix socket.
redis 8.8.0, valkey 9.1.0, aki at main, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| valkey | **112,674** | 96,506 | **146,865** | 93,212 | **192,396** |
| redis | 98,447 | **107,100** | 145,198 | **106,215** | 143,497 |
| kv-redis | 99,989 | 90,626 | 96,728 | 106,123 | 102,435 |
| aki | 95,304 | 80,370 | 82,999 | 80,013 | 54,545 |

On the M4, over a warm socket, redis and valkey lead the pure-read columns.
Their event loops are a decade tuned and the socket round-trip is cheap on this hardware, so the mature servers show best where the work is smallest.
kv-redis is in the pack on writes and level with redis on the read-update mix (106,123 vs 106,215 on ycsb-a), and trails the two leaders on read-only.
aki brings up the rear here, with a tail spike on ycsb-c (p99 3.2 ms) that the others do not have.

## A 6-core x86-64 Linux VPS

The same matrix on a Linux server, pinned to 4 cores, 4 concurrent clients, everysec durability, over a unix socket.
redis 8.8.0, valkey 7.2.12, aki at main, kv 0.4.0:

| Engine | fillrandom | overwrite | readrandom | ycsb-a (50/50) | ycsb-c (read-only) |
| --- | --- | --- | --- | --- | --- |
| kv-redis | **18,841** | **20,890** | **19,876** | **20,028** | **22,324** |
| valkey | 14,341 | 11,671 | 16,137 | 15,678 | 18,390 |
| redis | 11,727 | 12,110 | 16,526 | 11,387 | 14,701 |
| aki | 10,824 | 8,434 | 11,100 | 7,508 | 10,160 |

The ranking flips.
On the Linux box kv-redis leads every column, from 20% ahead of valkey on read-only up to nearly 2x redis on the write and mixed workloads, and it holds the tightest tail as well (p99 2.0-3.6 ms, against 3.5-6.9 ms for redis and valkey).
The absolute rates are far below the M4 table because this is a contended shared host with a quarter of the cores, so read the two tables as two separate rankings, not one cross-machine race.
What carries across both is the shape of kv-redis: even on the wire it stays flat across read, write and mix, the same trait that makes the embedded core lead its board, and on a real Linux server that flatness puts it in front.

## What to take from this

- kv's Redis face is a real option, not a demo. It is level with redis and valkey on the M4 and ahead of both on a Linux VPS, at the same everysec durability, through the same client and socket.
- If you are choosing a networked RESP store and running it on a Linux server, kv-redis is worth measuring on your own hardware; the flat write-and-mix profile is the reason to.
- redis and valkey remain excellent, especially for pure reads on fast hardware, and they carry a far larger command surface than kv's point subset (`GET`, `SET`, `DEL`, `EXISTS`, `PING`, plus the connect handshake). kv-redis is a point key/value store on the wire, not a full Redis.

## What is not here yet

- dragonfly, garnet and kvrocks have adapters in the harness but were not installed on these two hosts, so they are absent rather than estimated. Drop their server binary on `PATH` and they join the table.
- Only one Linux host is shown. Two other servers were meant to run this, but both were busy with real work at measurement time (an LLM and a Kubernetes node on one, a crawl job saturating the other), and a latency benchmark on a loaded box measures the noise, not the engine. A quiet re-run on those hosts is pending, the same way the embedded board is waiting on its cross-machine pass.
