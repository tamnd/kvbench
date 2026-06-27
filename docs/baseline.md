# Baseline

This is the reference run behind the table in the README, with the full
per-workload numbers and the durability contrast that explains them. It was taken
on a darwin/arm64 laptop (10 CPU, go 1.26.4), a busy dev machine, so the absolute
numbers are indicative rather than publishable; the shape and the ratios are the
point. Reproduce with the commands at the bottom.

All cells are single client (`--conc 1`) so the numbers are the structure and
engine cost without lock-scaling mixed in. Values are 1 KiB.

## Point baseline, durability OFF

50k keys, 100k measured ops, two reps, durability OFF so the write path shows
without the per-commit fsync floor. readrandom and overwrite draw uniformly over
the loaded keyspace; fillrandom inserts fresh keys. Throughput in ops/s.

| engine | readrandom | fillrandom | overwrite | class |
| --- | ---: | ---: | ---: | --- |
| devnull | 14,700,000 | 5,080,000 | 5,170,000 | floor |
| swiss | 8,730,000 | 2,160,000 | 2,060,000 | ceiling |
| otter | 8,260,000 | 2,090,000 | 2,290,000 | ceiling |
| memory | 6,900,000 | 1,830,000 | 2,260,000 | ceiling |
| faster | 5,140,000 | 905,000 | 1,030,000 | ceiling |
| pogreb | 1,710,000 | 170,000 | 190,000 | durable |
| buntdb | 1,430,000 | 251,000 | 293,000 | durable |
| bbolt | 871,000 | 45,000 | 47,000 | durable |
| kv-lsm | 780,000 | 99,000 | 92,000 | durable |
| kv-btree | 766,000 | 22,000 | 22,000 | durable |
| goleveldb | 604,000 | 117,000 | 118,000 | durable |
| kv-betree | 584,000 | 6,100 | 5,400 | durable |
| badger | 571,000 | 168,000 | 171,000 | durable |
| pebble | 481,000 | 155,000 | 155,000 | durable |
| sqlite | 51,000 | 28,000 | 27,000 | durable |

What it says:

The read ceiling for this keyspace is about 8.7M ops/s (swiss, a bare
open-addressing table). The fastest durable engine, kv-lsm at 780k, reads at about
a ninth of that. The distance is what an ordered, persistent, transactional engine
pays over a flat hash table: a tree descent instead of one probe, a key compare
that cannot stop at a hash match, snapshot bookkeeping, a page cache lookup. None
of those are waste, but the ceiling shows how much budget they consume.

devnull at 14.7M reads is the harness floor. No engine in this harness reads
faster, because once the store does nothing the remaining time is the workload
generator producing the next key and the latency clock recording the op. A real
engine's read number is that floor plus its own work, so the useful read ceiling a
store can chase is min(devnull, swiss) and the swiss number is the binding one
here.

Among the kv cores, kv-btree and kv-lsm read within a few percent of each other
and lead the durable field on reads alongside pogreb and buntdb. kv-betree reads in
the same band but writes far slower than either shipped core; that core is mid
rewrite and its write path is the open problem, not a property of kv as shipped.

## Why the write benchmark looks slow: the durability contrast

A separate small run (5k keys, 3k ops) sweeping the same fillrandom across the
three durability levels. Small N keeps every engine in its memtable so the OFF
number is the pure write-path cost with no compaction mixed in; that is why the OFF
column here reads higher than the 50k table above for the LSM engines.

| engine | OFF | NORMAL | FULL | OFF / FULL |
| --- | ---: | ---: | ---: | ---: |
| kv-btree | 29,400 | 30,800 | 249 | 118x |
| kv-lsm | 215,000 | 215,000 | 251 | 856x |
| kv-betree | 7,960 | 8,540 | 245 | 32x |
| bbolt | 46,700 | 125 | 125 | 374x |
| pebble | 1,430,000 | 1,420,000 | 252 | 5,700x |
| badger | 202,000 | 191,000 | 25,200 | 8x |

This is the answer to "why is the write benchmark so slow". At FULL durability every
engine that fsyncs per commit collapses to about 250 ops/s, which is not the engine,
it is this disk's fsync rate. A FULL write workload measures the storage device, and
every per-commit-fsync engine converges to the same floor regardless of how clever
its write path is. Run OFF to compare engines; run FULL to measure the durability
tax on a given disk.

Two engines break the pattern and both are informative:

bbolt's NORMAL is already a full fsync. Its NORMAL and FULL numbers are identical
(125 ops/s) because bbolt has no relaxed-durability mode; a NORMAL cell for bbolt is
a FULL cell. kv's NORMAL, by contrast, tracks its OFF number, because kv in WAL mode
does not fsync every commit at NORMAL. Comparing kv-NORMAL against bbolt-NORMAL would
compare a no-fsync path against an fsync-per-commit path, so the result carries that
asterisk.

badger stays fast at FULL (25k ops/s, an 8x drop rather than a 100x one) because it
groups commits and fsyncs the batch, not each write, so a single-client loop amortizes
the sync across the group. It is the one engine here whose FULL number still reflects
the engine rather than the disk.

## Reproduce

```
go build -o bin/kvbench ./cmd/kvbench

# point baseline, durability off
bin/kvbench run \
  --workloads readrandom,fillrandom,overwrite \
  --durability OFF --conc 1 --cardinality 50000 --ops 100000 --reps 2 \
  --out results/baseline

# durability contrast
bin/kvbench run \
  --engines kv-btree,kv-lsm,kv-betree,bbolt,pebble,badger \
  --workloads fillrandom \
  --durability OFF,NORMAL,FULL --conc 1 --cardinality 5000 --ops 3000 --reps 1 \
  --out results/durability-contrast
```

The in-memory ceilings and the devnull floor are registered in `adapters/inmem`
and compiled into the default binary, so they appear in `kvbench list` and run with
no extra flags.
