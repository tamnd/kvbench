#!/usr/bin/env bash
# run-all-fresh.sh runs the whole field plus both kv cores into one fresh results
# dir, so the comparison is captured under a single machine state. The external
# engines are pinned libraries whose numbers do not move between runs; kv is the
# one that changed (perf wave 4), so this re-measures everyone side by side rather
# than splicing a fresh kv into a stale field.
#
# Field engines run the full op budget. kv scans materialize the forward range at
# construction (spec 11), so a scan op is O(keyspace); kv scan-heavy workloads get
# a small op budget, called out here and in the result docs, not hidden. Throughput
# is ops/sec so the cells stay comparable.
set -u
cd "$(dirname "$0")"
export PATH="$PWD/bin:$PATH"
BIN=./bin/kvbench-all
OUT=results/run-2
mkdir -p "$OUT"

FIELD=badger,bbolt,buntdb,goleveldb,lmdb,memory,pebble,pogreb,sqlite,sled,fjall,redb,redis
KV=kv-btree,kv-lsm

READ_W=readrandom,readseq,ycsb-b,ycsb-c,ycsb-d
WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-e,ycsb-f
KV_POINT_READ=readrandom,ycsb-b,ycsb-c,ycsb-d
KV_SCAN_W=readseq,ycsb-e
KV_WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-f

ROPS=100000
WOPS=5000
KV_SOPS_CACHE=200
KV_SOPS_COLD=100

run() { echo ">>> $*"; $BIN run "$@" --out "$OUT" 2>&1 | grep -vE "pool.go|connection pool"; }

echo "################ FIELD ################"
echo "==== PLANE A: headline (cache-resident, NORMAL) ===="
run --engines "$FIELD" --workloads "$READ_W"  --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $ROPS --reps 2 --conc 8
run --engines "$FIELD" --workloads "$WRITE_W" --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $WOPS --reps 2 --conc 8
echo "==== PLANE B: durability (fillrandom OFF/NORMAL/FULL) ===="
run --engines "$FIELD" --workloads fillrandom --regimes cache-resident --durability OFF,NORMAL,FULL --cardinality 100000 --ops $WOPS --reps 2 --conc 8
echo "==== PLANE C: out-of-cache (NORMAL) ===="
run --engines "$FIELD" --workloads "$READ_W" --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $ROPS --reps 2 --conc 8
run --engines "$FIELD" --workloads fillrandom --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $WOPS --reps 2 --conc 8

echo "################ KV (kv-btree, kv-lsm) ################"
echo "==== PLANE A ===="
run --engines "$KV" --workloads "$KV_POINT_READ" --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $ROPS --reps 2 --conc 8
run --engines "$KV" --workloads "$KV_SCAN_W"     --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $KV_SOPS_CACHE --reps 2 --conc 8
run --engines "$KV" --workloads "$KV_WRITE_W"    --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $WOPS --reps 2 --conc 8
echo "==== PLANE B ===="
run --engines "$KV" --workloads fillrandom --regimes cache-resident --durability OFF,NORMAL,FULL --cardinality 100000 --ops $WOPS --reps 2 --conc 8
echo "==== PLANE C ===="
run --engines "$KV" --workloads "$KV_POINT_READ" --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $ROPS --reps 2 --conc 8
run --engines "$KV" --workloads readseq        --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $KV_SOPS_COLD --reps 2 --conc 8
run --engines "$KV" --workloads fillrandom     --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $WOPS --reps 2 --conc 8

echo "################ DONE ################"
echo "cells in $OUT: $(ls "$OUT" | wc -l)"
