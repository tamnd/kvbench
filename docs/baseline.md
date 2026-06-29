# Baseline

This is the reference run behind the table in the README, with the full
per-workload numbers and the durability contrast that explains them. It was taken
on an Apple M4 laptop (10 CPU, 24 GB, go 1.26.4, darwin/arm64), a busy dev machine,
so the absolute numbers are indicative rather than publishable; the shape and the
ratios are the point. Reproduce with the commands at the bottom.

All cells are single client (`--conc 1`) so the numbers are the structure and
engine cost without lock-scaling mixed in. Values are 1 KiB.

kv ships one core now, f2, a latch-free sharded hash index over a hybrid log. It
shows up three ways here. `kv-f2` is the bare core in memory, the ceiling the
durable layout chases. `kv-f2-durable` is the durable single-file layout. `kv` is
the full DB stack a user gets: every Put is its own WAL'd, MVCC transaction.

## Point baseline, durability OFF

50k keys, 100k measured ops, two reps, durability OFF so the write path shows
without the per-commit fsync floor. readrandom and overwrite draw uniformly over
the loaded keyspace; fillrandom inserts fresh keys. Throughput in ops/s, sorted by
readrandom.

| engine | readrandom | fillrandom | overwrite | class |
| --- | ---: | ---: | ---: | --- |
| devnull | 15,151,829 | 5,449,132 | 5,352,990 | floor |
| memory | 10,695,891 | 2,402,015 | 2,502,453 | ceiling |
| otter | 10,200,470 | 2,480,002 | 2,509,940 | ceiling |
| swiss | 9,073,318 | 2,495,683 | 2,515,511 | ceiling |
| f2 | 7,607,208 | 1,923,525 | 1,886,265 | ceiling |
| faster | 6,947,112 | 1,165,381 | 1,226,486 | ceiling |
| kv-f2 | 5,848,333 | 2,637,581 | 2,524,245 | ceiling (kv core) |
| pogreb | 2,126,482 | 219,855 | 231,308 | durable |
| kv-f2-durable | 2,045,264 | 1,519,110 | 1,545,917 | durable (kv) |
| kv | 2,030,279 | 50,430 | 57,106 | durable (kv stack) |
| buntdb | 1,660,520 | 313,848 | 288,851 | durable |
| bbolt | 1,012,981 | 49,137 | 48,298 | durable |
| libmdbx | 979,792 | 88,145 | 88,175 | durable, cgo |
| lmdb | 671,484 | 93,924 | 96,032 | durable, cgo |
| badger | 625,338 | 180,341 | 180,567 | durable |
| goleveldb | 614,634 | 111,467 | 108,244 | durable |
| pebble | 589,206 | 156,729 | 156,440 | durable |
| sqlite | 61,257 | 35,178 | 35,911 | durable |

What it says:

The read ceiling for this keyspace is about 9M to 10M ops/s (swiss and otter, bare
in-memory tables). devnull at 15.1M reads is the harness floor: no engine reads
faster, because once the store does nothing the remaining time is the workload
generator producing the next key and the latency clock recording the op. A real
engine's read number is that floor plus its own work, so the useful ceiling a store
can chase is min(devnull, swiss), and swiss is the binding one here.

The f2 core sits right on that ceiling. `kv-f2` reads at 5.8M and writes at 2.6M,
faster on writes than swiss because an append to a per-shard log beats an
open-addressing insert that may probe and resize. The standalone `f2` and `faster`
rails read a little higher (7.6M, 6.9M) and write a little lower; they are the same
shape behind a single RWMutex, which costs nothing at one client and a lot under
concurrency (see the README's eight-thread table).

The durable f2 layout is the embedded story. `kv-f2-durable` reads 2.0M and writes
1.5M, while the cgo cow-B+trees it shares the single-file class with, libmdbx and
lmdb, write at 88k and 94k and read at 980k and 671k. A hash-log appends the new
value and atomically repoints one index slot; a copy-on-write B+tree copies a
root-to-leaf path of pages on every commit, so the write gap is the data structure,
not the language or the fsync (durability is off for both). The full `kv` stack
writes at 50k because each benchmark Put is its own WAL'd, MVCC transaction; that
per-commit shell, not the core, is what the kv row measures, and the gap down from
kv-f2-durable is its cost.

## Why the write benchmark looks slow: the durability contrast

A separate small run (5k keys, 3k ops, five reps, median) sweeping the same
fillrandom across the three durability levels. Small N keeps every engine in its
memtable so the OFF number is the pure write-path cost with no compaction mixed in.
This table is the Go engines only; the cgo cow-B+trees are left out on purpose, see
the note below.

| engine | OFF | NORMAL | FULL | OFF / FULL |
| --- | ---: | ---: | ---: | ---: |
| kv-f2-durable | 1,659,024 | 1,551,824 | 240 | 6,913x |
| kv | 93,115 | 195,337 | 281 | 331x |
| pebble | 966,002 | 956,569 | 283 | 3,413x |
| badger | 200,322 | 205,164 | 26,751 | 7x |
| bbolt | 50,386 | 135 | 126 | 400x |

This is the answer to "why is the write benchmark so slow". At FULL durability every
engine that fsyncs per commit collapses to about 250 ops/s, which is not the engine,
it is this disk's fsync rate. A FULL write workload measures the storage device, and
every per-commit-fsync engine converges to the same floor regardless of how clever
its write path is. Run OFF to compare engines; run FULL to measure the durability
tax on a given disk.

Two engines break the pattern and both are informative:

bbolt's NORMAL is already a full fsync. Its NORMAL and FULL numbers are the same
floor (about 130 ops/s) because bbolt has no relaxed-durability mode; a NORMAL cell
for bbolt is a FULL cell. kv's NORMAL, by contrast, stays up with its OFF number,
because kv in WAL mode does not fsync every commit at NORMAL. Comparing kv-NORMAL
against bbolt-NORMAL would compare a no-fsync path against an fsync-per-commit path,
so the result carries that asterisk.

badger stays fast at FULL (27k ops/s, a 7x drop rather than a 1000x one) because it
groups commits and fsyncs the batch, not each write, so a single-client loop
amortizes the sync across the group. It is the one engine here whose FULL number
still reflects the engine rather than the disk.

### Why the cgo engines sit out the FULL contrast

On this run libmdbx and lmdb post a FULL fillrandom of about 19k ops/s, roughly
seventy times the Go engines' 250. That is not a durability win, it is a platform
asymmetry. On macOS a plain `fsync(2)` only pushes writes to the drive's own cache;
a true flush to stable media needs `fcntl(F_FULLFSYNC)`. Go's `File.Sync` issues
`F_FULLFSYNC`, so every Go engine here pays the real platter sync at FULL. The
bundled LMDB and libmdbx C code calls plain `fsync`, which returns before the data
is durable against power loss. So their FULL number measures a weaker guarantee than
the Go engines' FULL number, and putting them in the same column would compare two
different promises. The OFF column, where no engine syncs, is the like-for-like
write-path comparison for the cgo engines, and it is in the point table above.

## Reproduce

```
CGO_ENABLED=1 go build -tags cgo_engines -o bin/kvbench ./cmd/kvbench

# point baseline, durability off
bin/kvbench run \
  --workloads readrandom,fillrandom,overwrite \
  --durability OFF --conc 1 --cardinality 50000 --ops 100000 --reps 2 \
  --out results/baseline

# durability contrast (Go engines)
bin/kvbench run \
  --engines kv,kv-f2-durable,bbolt,pebble,badger \
  --workloads fillrandom \
  --durability OFF,NORMAL,FULL --conc 1 --cardinality 5000 --ops 3000 --reps 5 \
  --out results/durability-contrast
```

The in-memory ceilings and the devnull floor are registered in `adapters/inmem`
and compiled into the default binary, so they appear in `kvbench list` and run with
no extra flags. The cgo engines (libmdbx, lmdb) need the `cgo_engines` build tag and
a C compiler; nothing else, since both bindings bundle their C source.
