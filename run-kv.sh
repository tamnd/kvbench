#!/usr/bin/env bash
# run-kv.sh adds the two kv cores (kv-btree, kv-lsm) to the full matrix, writing
# into the same results/full dir so they join the rest of the field. kv is pure
# Go and in-process, so this needs no build tags.
#
# It mirrors run-full.sh's three planes and op budgets, with one difference:
# kv's iterator materializes the forward range at construction (spec 11; the
# streaming form is a later milestone), so a single scan op pays to build the
# whole tail from its start key. At 100k-300k keys that is tens to hundreds of MB
# per op, so running scans at the 100k-op read budget would copy terabytes for no
# extra signal. Scan-heavy workloads (readseq, ycsb-e) therefore get a small op
# budget here. Throughput is ops/sec, so the cells stay comparable; the smaller
# budget is called out in the result docs, not hidden.
set -u
cd "$(dirname "$0")"
BIN=./bin/kvbench-kv
OUT=results/full
mkdir -p "$OUT"

go build -o "$BIN" ./cmd/kvbench || exit 1

ENG=kv-btree,kv-lsm

POINT_READ=readrandom,ycsb-b,ycsb-c,ycsb-d
SCAN_W=readseq,ycsb-e
WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-f

ROPS=100000
WOPS=5000
# kv scans are O(keyspace) per op (eager materialization), so they run at a few
# ops/sec at this scale. A couple hundred ops is plenty to pin the throughput,
# and a larger budget would add many minutes with no new signal.
SOPS_CACHE=200
SOPS_COLD=100

run() { echo ">>> $*"; $BIN run "$@" --out "$OUT" 2>&1 | grep -vE "pool.go|connection pool"; }

echo "================ PLANE A: headline (cache-resident, NORMAL) ================"
run --engines "$ENG" --workloads "$POINT_READ" --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $ROPS --reps 2 --conc 8
run --engines "$ENG" --workloads "$SCAN_W"     --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $SOPS_CACHE --reps 2 --conc 8
run --engines "$ENG" --workloads "$WRITE_W"    --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $WOPS --reps 2 --conc 8

echo "================ PLANE B: durability (cache-resident, fillrandom, OFF/NORMAL/FULL) ================"
run --engines "$ENG" --workloads fillrandom --regimes cache-resident --durability OFF,NORMAL,FULL --cardinality 100000 --ops $WOPS --reps 2 --conc 8

echo "================ PLANE C: out-of-cache (NORMAL) ================"
run --engines "$ENG" --workloads "$POINT_READ" --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $ROPS --reps 2 --conc 8
run --engines "$ENG" --workloads readseq        --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $SOPS_COLD --reps 2 --conc 8
run --engines "$ENG" --workloads fillrandom     --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $WOPS --reps 2 --conc 8

echo "================ DONE ================"
echo "cells now in $OUT: $(ls "$OUT" | wc -l)"
