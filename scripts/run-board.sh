#!/usr/bin/env bash
# run-board.sh drives the canonical pure-Go embedded field into one fresh results
# dir, so the whole comparison is captured under a single machine state. Every
# engine goes through the same Engine SPI with no special path, kv included.
#
# The field is the eight pure-Go single-binary embedded stores: no cgo, no server,
# nothing to install. kv is measured as its hlog storage core (a sharded resident
# hash index over a hybrid log), so it is unordered and the harness skips the sorted
# scan workloads (readseq, ycsb-e) for it automatically.
#
# The op budget is tiered. Read workloads run a large op count for a stable rate.
# Write workloads run far fewer, because an engine that fsyncs on every commit
# (bbolt at its shipped default, and every engine under FULL) settles around a
# hundred ops per second on a laptop disk, so a large write budget would take hours
# with no extra signal. The load that precedes each measured phase is batched, so it
# stays fast for every engine regardless of the measured op count. Throughput is
# ops/sec, so cells with different budgets stay comparable.
#
# Two durability regimes, both durable. DEFAULT runs each engine at its own shipped
# durability, the honest out-of-the-box comparison. FULL forces a per-commit fsync
# on every engine, so the background-committing engines pay the disk too.
set -u
cd "$(dirname "$0")/.."
BIN=./bin/kvbench
OUT=results/run-1

go build -o "$BIN" ./cmd/kvbench || exit 1

FIELD=badger,bbolt,buntdb,goleveldb,kv,pebble,pogreb,sqlite
READ_W=readrandom,readseq,ycsb-b,ycsb-c,ycsb-d
WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-e,ycsb-f
ROPS=100000
WOPS=5000

run() { echo ">>> $*"; "$BIN" run "$@" --out "$OUT" 2>&1 | grep -vE "pool.go|connection pool"; }

echo "================ PLANE A: headline (cache-resident, DEFAULT) ================"
run --engines "$FIELD" --workloads "$READ_W"  --regimes cache-resident --durability DEFAULT --cardinality 100000 --ops $ROPS --reps 2 --conc 8
run --engines "$FIELD" --workloads "$WRITE_W" --regimes cache-resident --durability DEFAULT --cardinality 100000 --ops $WOPS --reps 2 --conc 8

echo "================ PLANE B: durability (cache-resident, fillrandom+overwrite, DEFAULT/FULL) ================"
# The durability plane loads a smaller keyset into its own results dir. Under FULL
# every engine fsyncs on every commit, so the batched load that precedes the measured
# phase also pays a per-commit fsync, and at 100k keys that load runs for tens of
# minutes on a laptop disk with no extra signal in the durable-write rate. 10k keys
# reaches the same steady rate in a tractable time, so this is the cardinality behind
# the durable-writes page. It stays out of results/run-1 so it never shares a table
# with the 100k headline plane.
"$BIN" run --engines "$FIELD" --workloads fillrandom,overwrite --regimes cache-resident --durability DEFAULT,FULL --cardinality 10000 --ops 2000 --reps 2 --conc 8 --out results/durable 2>&1 | grep -vE "pool.go|connection pool"

echo "================ PLANE C: out-of-cache (DEFAULT) ================"
run --engines "$FIELD" --workloads "$READ_W"  --regimes out-of-cache --durability DEFAULT --cardinality 300000 --ops $ROPS --reps 2 --conc 8
run --engines "$FIELD" --workloads fillrandom --regimes out-of-cache --durability DEFAULT --cardinality 300000 --ops $WOPS --reps 2 --conc 8

echo "================ DONE ================"
echo "cells in $OUT: $(ls "$OUT" | wc -l)"
