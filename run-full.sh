#!/usr/bin/env bash
# run-full.sh drives the whole kvbench matrix across every engine and every
# execution mode (in-proc, cgo, subprocess, network), three durability levels,
# and both cache regimes.
#
# The op budget is tiered. Read workloads run a large number of ops for stable
# numbers. Write workloads run far fewer, because engines that fsync on every
# commit (bbolt always, most engines at FULL durability) settle around 100 ops/s
# on macOS, so a large write budget would take hours with no extra signal. The
# database load that precedes each measured phase is batched, so it stays fast
# for every engine regardless of the measured op count. Throughput is ops/sec,
# so cells with different op budgets are still comparable.
set -u
cd "$(dirname "$0")"
export PATH="$PWD/bin:$PATH"
BIN=./bin/kvbench-all
OUT=results/full
mkdir -p "$OUT"

ALL=badger,bbolt,buntdb,goleveldb,lmdb,memory,pebble,pogreb,sqlite,sled,fjall,redb,redis

READ_W=readrandom,readseq,ycsb-b,ycsb-c,ycsb-d
WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-e,ycsb-f

ROPS=100000
WOPS=5000

run() { echo ">>> $*"; $BIN run "$@" --out "$OUT" 2>&1 | grep -vE "pool.go|connection pool"; }

echo "================ PLANE A: headline (cache-resident, NORMAL) ================"
run --engines "$ALL" --workloads "$READ_W"  --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $ROPS --reps 2 --conc 8
run --engines "$ALL" --workloads "$WRITE_W" --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $WOPS --reps 2 --conc 8

echo "================ PLANE B: durability (cache-resident, fillrandom, OFF/NORMAL/FULL) ================"
run --engines "$ALL" --workloads fillrandom --regimes cache-resident --durability OFF,NORMAL,FULL --cardinality 100000 --ops $WOPS --reps 2 --conc 8

echo "================ PLANE C: out-of-cache (NORMAL) ================"
run --engines "$ALL" --workloads "$READ_W" --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $ROPS --reps 2 --conc 8
run --engines "$ALL" --workloads fillrandom --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $WOPS --reps 2 --conc 8

echo "================ DONE ================"
echo "cells: $(ls "$OUT" | wc -l)"
