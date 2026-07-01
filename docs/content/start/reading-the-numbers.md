---
title: "How to read the numbers"
linkTitle: "Reading the numbers"
description: "Throughput, tail latency, durability mode, and space amplification: the four numbers that decide which key/value engine fits, and how each one can mislead you."
weight: 20
---

A benchmark number is only useful if you know what it leaves out.
Four numbers decide which engine fits, and each one hides a trap.

## Throughput: operations per second

Throughput is how many `put` or `get` calls the engine finishes per second.
Bigger is better, and the spread is huge: the engines here range from 52,000 to nearly 7 million reads per second on the same machine.

The trap is the warm-up burst.
An engine can look fast for the first second while its buffers are empty, then collapse when the background work catches up.
kvbench reports throughput sustained over the whole measured window, after a warm-up, so the number is the rate you can keep, not the rate you can touch.

## Tail latency: the p99

The average latency lies.
If 99 requests take 1 microsecond and one takes 100 milliseconds, the average still looks great, but that one slow request is the one your user notices.

So this site reports the **p99**: the time below which 99 out of 100 operations finish.
A low p99 means the engine is consistent.
A low average with a high p99 means it stalls now and then, usually for background compaction or a page flush.
For anything user-facing, the p99 matters more than the throughput.

kvbench measures latency open-loop, at a steady arrival rate, so a stall lands in the tail where you can see it instead of slowing the next request down and hiding.

## Durability: was the write actually saved?

This is the one that makes benchmarks dishonest, so read it carefully.

When you `put` a key, the engine decides when the write reaches the disk:

- Flush on every commit, before returning. Slow, because it waits on the hardware, but an acknowledged write survives a power cut with zero loss.
- Acknowledge the write and flush a moment later on a short timer. Fast, because the ack does not wait on the disk, and a crash loses at most the last sub-second sliver, not the whole store. This is what Redis does with appendfsync everysec, and it is durable, just on a small delay.

That flush is the single most expensive thing a storage engine does.
An engine that flushes on every commit runs hundreds of writes per second; the same engine flushing on a timer runs into the millions.
So comparing one engine's per-commit write against another's timer-flush write is meaningless, and a lot of published benchmarks quietly do exactly that.

kvbench refuses to mix them.
Every write workload is run in two regimes:

- **DEFAULT:** every engine at its own shipped durability. Some flush per commit, some on a timer, none lose data outright. This is the honest out-of-the-box comparison.
- **FULL:** every engine flushes on every commit. This measures the real cost of zero-loss durability, same rules for everyone.

The two never share a table.
When you see a write number, the heading tells you which regime it is.
The [durable-writes scenario](/scenarios/durable-writes/) is entirely about the FULL numbers, because that is what a database that must never lose a write actually pays.

## Space amplification: bytes on disk per byte of data

If you store 100 MB of values, how much disk does the engine use?
Space amplification is the answer as a multiple: 1.0x is break-even, below 1.0x means the engine compressed your data, above means overhead.

The spread is wide and surprising.
The LSM engines compress 1 KB values down to a fraction of their size (as low as 0.11x).
One engine here uses **7x** the raw data right after a write-heavy run because its design keeps old copies around until a background job reclaims them.
Space is cheap until you are paying for it by the gigabyte-month, at which point a 100x difference between two engines is a real bill.

## Putting it together

No engine wins all four.
The fastest reader gives up ordered scans and durable per-commit throughput.
The smallest on disk is mid-pack on reads.
Pick the two numbers your workload actually cares about, then read the [scenario](/scenarios/) that matches.
