#!/usr/bin/env sh
# Portable cross-host benchmark profile for the pure-Go embedded engines.
#
# It runs the same matrix on every host (laptop or server) so the numbers line
# up across machines. The default build is pure Go with no cgo, so a single
# static binary runs anywhere without installing a toolchain or any engine.
#
# Two passes, both in a SINGLE durability mode shared by every engine, so the
# numbers are directly comparable. We never use each engine's shipped default,
# because those defaults disagree: bbolt and kv fsync every commit, badger and
# pebble batch in the background. Comparing one engine's durable number against
# another's async number is the classic benchmark lie. So:
#
#   raw pass      durability OFF on every engine: no fsync, the OS owns the
#                 flush. The structural speed of the engine, same rules for all.
#                 Finishes in seconds.
#
#   durable pass  durability FULL on every engine: an fsync on every commit, same
#                 rules for all. This settles at hundreds of ops per second
#                 because it is disk-bound, so a few thousand writes is enough to
#                 read a steady rate; a large op count would take ten minutes a
#                 cell for nothing.
#
# Usage: scripts/bench-profile.sh <bin> <out-dir>
set -eu

BIN="${1:?usage: bench-profile.sh <bin> <out-dir>}"
OUT="${2:?usage: bench-profile.sh <bin> <out-dir>}"

ENGINES="badger,bbolt,buntdb,goleveldb,kv,pebble,pogreb,sqlite"
CARD=100000
VAL=1024
CONC=8

echo "raw pass (durability OFF) -> $OUT"
"$BIN" run \
	--engines "$ENGINES" \
	--workloads fillrandom,overwrite,readrandom,readseq,ycsb-a,ycsb-b,ycsb-c,ycsb-f \
	--regimes cache-resident \
	--durability OFF \
	--values "$VAL" --conc "$CONC" --cardinality "$CARD" --ops 100000 --reps 2 --seed 42 \
	--out "$OUT"

echo "durable pass (durability FULL) -> $OUT"
"$BIN" run \
	--engines "$ENGINES" \
	--workloads fillrandom,overwrite \
	--regimes cache-resident \
	--durability FULL \
	--values "$VAL" --conc "$CONC" --cardinality "$CARD" --ops 4000 --reps 1 --seed 42 \
	--out "$OUT"

echo "done -> $OUT"
