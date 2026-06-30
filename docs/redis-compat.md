# The Redis-compatible rail

Several of the engines kvbench measures are not embedded libraries at all: they are separate server processes that speak the Redis wire protocol.
The harness reaches each one the same way, over a private unix socket with the pure-Go go-redis client, so a difference in the number is the server, not the adapter.
This page is the comparison between them, and where kv's own Redis face lands among them.

## The engines

| engine | what it is | persistence |
| --- | --- | --- |
| redis | Redis 8.8, the original | append-only file, `appendfsync everysec` by default |
| valkey | Valkey 9.1, the Redis fork | the same append-only log, same once-a-second fsync |
| dragonfly | Dragonfly, a shared-nothing multi-threaded core | periodic snapshots, no per-command fsync |
| garnet | [Microsoft Garnet](https://github.com/microsoft/garnet), a RESP cache-store on the FASTER core | in-memory, optional checkpoints plus an append-only file |
| kv-redis-cache | [tamnd/kv](https://github.com/tamnd/kv)'s `serve :memory:` Redis face | none; the in-memory backend is a pure RAM cache, gone on exit |
| aki | [tamnd/aki](https://github.com/tamnd/aki), a Redis-compatible database in a single file | a paged b-tree store in one file plus its WAL |
| kvrocks | [Apache Kvrocks](https://github.com/apache/kvrocks), a RESP face on RocksDB | a RocksDB directory of SST files plus its WAL |
| kv-redis | [tamnd/kv](https://github.com/tamnd/kv)'s `serve` Redis face | kv's single-file hash-log with its WAL |

redis, valkey, dragonfly, garnet and kv-redis-cache are the in-memory reference: at their core the keyspace is a RAM structure, and there is no disk in the read or write path.
aki, kvrocks and kv-redis are the persistent relatives: the data lives in an on-disk store, and a read or a write touches that store, not only RAM.
That difference is the whole point of putting them on one board, and it is why the report splits the in-memory servers (Class 2) from the persistent ones (Class 3).

kv shows up on both sides on purpose, as the same Redis face over two backends.
kv-redis-cache is `kv serve :memory:`, the keyspace held in kv's in-memory backend with nothing written to disk, which is what puts it in Class 2 next to redis and valkey.
kv-redis is `kv serve <db>`, the same wire loop over an on-disk hash-log that commits every write, which is what puts it in Class 3 next to aki and kvrocks.
The pair is the cost of durability measured on one engine: the gap between the two columns is what a committed write buys over a RAM set.

## Running it

The RESP adapters are behind the `network_engines` build tag, and each needs its server binary on PATH at run time (the adapter launches it):

```
go build -tags network_engines -o kvbench-net ./cmd/kvbench
# redis-server / valkey-server / dragonfly / GarnetServer / aki / kvrocks / kv on PATH as needed
kvbench-net run --engines valkey,aki,kv-redis \
  --workloads fillrandom,readrandom,overwrite,deleterandom,ycsb-a,ycsb-b,ycsb-c,ycsb-f \
  --regimes cache-resident --durability OFF \
  --values 1024 --conc 8 --cardinality 100000 --ops 100000 --reps 2 --seed 42 \
  --out results/resp
kvbench-net report --in results/resp --md
```

Build the kv binary from [tamnd/kv](https://github.com/tamnd/kv) with `go build -o kv ./cmd/kv`; the kv-redis adapter runs `kv serve <db> --addr "" --resp-unixsocket <sock>`, which turns the HTTP face off and serves only RESP on the socket, and the kv-redis-cache adapter runs the same with `:memory:` in place of the db path and `--synchronous off`, which opens kv on its in-memory backend so nothing touches the disk.

The scan workloads (`readseq`, `ycsb-e`) are left out: the RESP string keyspace is unordered, so a sorted scan is not part of the contract for any of these engines.

## Why this run is at OFF durability

The Redis-compatible engines do not agree on what their default durability means, and on this hardware the disagreement swamps everything else.
redis, valkey and aki default to fsyncing about once a second; dragonfly snapshots periodically; kv ships fsync-on-every-commit.
On macOS the gap is even wider, because a true per-commit sync goes through `F_FULLFSYNC` (a real platter flush, a few hundred a second) while a once-a-second append-log fsync costs almost nothing in the steady state.
Putting a per-commit-fsync engine next to a once-a-second-fsync engine under one column would measure the disk's flush rate, not the servers.

So this board is taken at `--durability OFF`: the per-commit barrier is removed from every engine, and what is left is the storage engine and the wire path.
It is the honest "how fast is the server itself" comparison.
The durability each engine actually ships, and what it costs, is in [the public profile's fairness table](public-benchmark.md#fairness-default-durability).

A note on kv-redis specifically: its shipped default is `SyncFull`, an fsync on every commit, so its `DEFAULT` and `FULL` numbers on macOS are the `F_FULLFSYNC`-per-commit floor, the same disk-bound rate every per-commit engine converges on, not a number that says anything about the engine.
That is why the comparison here is at OFF.

## The board

Apple M4, darwin/arm64, 10 CPU, 24 GB. 1 KiB values, 100k keyspace, 8 client connections, `--durability OFF`, two repetitions, the steady-state figure kept.
Throughput in operations per second; higher is better.

| workload | valkey | aki | kv-redis |
| --- | ---: | ---: | ---: |
| fillrandom (write) | 185,498 | 137,910 | 37,385 |
| overwrite (write) | 188,628 | 132,635 | 36,519 |
| deleterandom (write) | 195,610 | 134,851 | 46,622 |
| ycsb-a (50/50 read/update) | 179,090 | 134,763 | 55,394 |
| ycsb-f (read-modify-write) | 201,630 | 120,957 | 64,446 |
| ycsb-b (95/5 read-heavy) | 199,008 | 136,936 | 101,313 |
| readrandom (read) | 204,047 | 137,955 | 134,378 |
| ycsb-c (read-only) | 193,738 | 134,592 | 136,980 |

redis is not in this table on purpose: on the macOS host the `redis-server` on PATH is a Homebrew symlink to the Valkey binary, so a "redis" row would be the valkey row relabeled.
redis 8.8, dragonfly, garnet and kvrocks are measured on the Linux bench host, where each ships a native build; dragonfly has no macOS build at all, garnet is reached through its .NET build, and kvrocks is built there against its bundled RocksDB.
The board above is the macOS point baseline, so it stays at the engines that run natively on the laptop; the bench-host servers are added to the Class 2 and Class 3 tables on that host.

## kv in both classes

kv-redis-cache and kv-redis are the same Redis face over two backends, so the cleanest way to read them is side by side from one run.
Same host and profile as the board above, both at `--durability OFF`, the two columns measured back to back.

| workload | kv-redis-cache (Class 2, RAM) | kv-redis (Class 3, on-disk) |
| --- | ---: | ---: |
| fillrandom (write) | 40,410 | 39,503 |
| overwrite (write) | 40,921 | 40,143 |
| deleterandom (write) | 70,250 | 48,321 |
| ycsb-a (50/50 read/update) | 52,258 | 55,193 |
| ycsb-f (read-modify-write) | 63,077 | 66,073 |
| ycsb-b (95/5 read-heavy) | 108,188 | 100,925 |
| readrandom (read) | 134,260 | 135,865 |
| ycsb-c (read-only) | 134,224 | 133,656 |

At OFF durability the two faces run neck and neck on almost every workload, and that is the expected result, not a flat run.
With the per-commit barrier off, the on-disk face's writes land in the OS page cache and the in-memory face's land in kv's RAM buffer, and on this machine those cost about the same, so what is left is one identical wire loop and one identical hash-log apply over two byte stores.
deleterandom is the one workload where the cache pulls clearly ahead, 70k against 48k, because the on-disk delete still appends its tombstone to a real file while the in-memory one does not.
The separation the two columns are built to show is not at OFF at all: it opens up when kv-redis runs at its shipped durability and pays an `F_FULLFSYNC` per commit, where the cache, having no durability to provide, keeps the OFF rate.
That is the point of carrying kv in both classes: kv-redis-cache is the honest Class 2 entry, a pure RAM cache with the disk out of the path, and the distance to it is what a committed write costs on the same engine.

This pair is also what proved out the in-memory backend: before kv's memfs grew its files with amortized capacity, the cache wrote an order of magnitude slower than the on-disk face, a backwards result that was a buffer-reallocation artifact rather than the engine, and the column above is what it looks like once that is fixed.

## Reading the board

The table is sorted from the most write-bound workload at the top to the most read-bound at the bottom, because that axis is the whole story for kv-redis.

On reads, kv-redis keeps pace.
Read-only `ycsb-c` and point `readrandom` put it at 137k and 134k, even with aki and about two-thirds of valkey.
A point read on kv's hash-log is a hash lookup and one log read, which is the same shape of work aki does and not far off what an in-memory server does once the value still has to cross the socket.

On writes, kv-redis pays for what it is.
`fillrandom` and `overwrite` sit at about 37k, roughly a fifth of valkey and a quarter of aki.
Every write here is a full kv commit: the value goes through the write-ahead log, the hash-log apply, and the change-feed publish, where valkey sets a slot in a RAM hash table and redis appends to a buffer.
At the median the gap is smaller than the throughput suggests, kv-redis writes land around 210µs p50 against valkey's 105µs, but the p99 stretches to about 15ms, because the per-command commits serialize through one group-commit leader and each one allocates.
The mixed YCSB workloads fall exactly where that split predicts: as the read fraction climbs from `ycsb-a` through `ycsb-b`, kv-redis climbs with it, from 55k to 101k.

The honest summary is that kv-redis is a persistent single-file store wearing a Redis face, not an in-memory cache.
It reads like one of the fast servers and writes like a database committing every key, which is what it is.
The write path is the obvious place to spend the next round of work: a leaner single-key blind commit that skips the per-command batch bookkeeping, and coalescing the group-commit leader's bookkeeping so the p99 tail comes in.
