#!/usr/bin/env bash
# run-kv-clean.sh re-measures only the kv cells, on an idle machine, at reps=3,
# overwriting the kv-btree/kv-lsm cells in results/run-2. The full-field pass put
# the kv section last, after ~90 minutes of sustained load, and the kv read cells
# came back high-variance (one kv-btree readrandom landed at 814k ops/s, an outlier
# that does not reproduce at ~74k on an idle box). The field cells are stable run to
# run; only kv needed re-measuring clean. Same op budgets as the field planes so the
# rows stay comparable; reps lifted to 3 so an outlier rep shows up in min/max.
set -u
cd "$(dirname "$0")"
BIN=./bin/kvbench-kv
OUT=results/run-2
mkdir -p "$OUT"
go build -o "$BIN" ./cmd/kvbench || exit 1

KV=kv-btree,kv-lsm
KV_POINT_READ=readrandom,ycsb-b,ycsb-c,ycsb-d
KV_SCAN_W=readseq,ycsb-e
KV_WRITE_W=fillseq,fillrandom,overwrite,deleterandom,ycsb-a,ycsb-f
ROPS=100000
WOPS=5000
KV_SOPS_CACHE=200
KV_SOPS_COLD=100

run() { echo ">>> $*"; $BIN run "$@" --out "$OUT" 2>&1 | grep -vE "pool.go|connection pool"; }

echo "==== KV PLANE A (cache-resident, NORMAL) ===="
run --engines "$KV" --workloads "$KV_POINT_READ" --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $ROPS --reps 3 --conc 8
run --engines "$KV" --workloads "$KV_SCAN_W"     --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $KV_SOPS_CACHE --reps 3 --conc 8
run --engines "$KV" --workloads "$KV_WRITE_W"    --regimes cache-resident --durability NORMAL --cardinality 100000 --ops $WOPS --reps 3 --conc 8
echo "==== KV PLANE B (durability) ===="
run --engines "$KV" --workloads fillrandom --regimes cache-resident --durability OFF,NORMAL,FULL --cardinality 100000 --ops $WOPS --reps 3 --conc 8
echo "==== KV PLANE C (out-of-cache) ===="
run --engines "$KV" --workloads "$KV_POINT_READ" --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $ROPS --reps 3 --conc 8
run --engines "$KV" --workloads readseq        --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $KV_SOPS_COLD --reps 3 --conc 8
run --engines "$KV" --workloads fillrandom     --regimes out-of-cache --durability NORMAL --cardinality 300000 --ops $WOPS --reps 3 --conc 8
echo "==== DONE ===="