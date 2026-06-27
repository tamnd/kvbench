# kvbench is pure Go with no cgo in the default build, so every target here runs
# with CGO_ENABLED=0. The cgo-only adapters (LMDB) sit behind the cgo_engines
# build tag and are built by the `cgo` target.

GO ?= go
PKG ?= ./...

.PHONY: build test race lint vet fmt fmt-check tidy vuln bench bench-public smoke cgo clean

build:
	CGO_ENABLED=0 $(GO) build ./...

# Build the runner binary into bin/.
bin/kvbench:
	CGO_ENABLED=0 $(GO) build -o bin/kvbench ./cmd/kvbench

test:
	CGO_ENABLED=0 $(GO) test -count=1 $(PKG)

# The race detector needs cgo; the pure-Go build above already proves the
# CGO_ENABLED=0 path compiles.
race:
	CGO_ENABLED=1 $(GO) test -race -count=1 $(PKG)

vet:
	CGO_ENABLED=0 $(GO) vet $(PKG)

fmt:
	gofmt -s -w .

fmt-check:
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt -s -w:"; echo "$$unformatted"; exit 1; \
	fi

lint:
	golangci-lint run

tidy:
	$(GO) mod tidy

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest $(PKG)

# A fast end-to-end check that the harness still runs: a tiny sweep over the
# in-memory floor and ceilings plus the kv cores, durability off.
smoke: bin/kvbench
	./bin/kvbench run \
		--engines devnull,swiss,otter,faster,kv-btree,kv-lsm,kv-betree \
		--workloads readrandom,fillrandom \
		--durability OFF --conc 1 --cardinality 5000 --ops 10000 --reps 1 \
		--out results/smoke

# The pinned public profile: the recognized YCSB A-F and db_bench workloads at a
# fixed seed and fixed sizes, every engine at its shipped durability (DEFAULT), so
# anyone can reproduce the same matrix and verify the numbers. The generators are
# deterministic and the dependency versions are locked in go.sum, so the only
# variable left is the hardware. See docs/public-benchmark.md for the full
# definition and how to read the result. Override OUT= to pick a results dir.
PUBLIC_OUT ?= results/public
bench-public: bin/kvbench
	./bin/kvbench run \
		--workloads fillseq,fillrandom,overwrite,readrandom,readseq,deleterandom,ycsb-a,ycsb-b,ycsb-c,ycsb-d,ycsb-e,ycsb-f \
		--regimes cache-resident \
		--durability DEFAULT \
		--values 1024 --conc 8 --cardinality 100000 --ops 200000 --reps 3 --seed 42 \
		--out $(PUBLIC_OUT)
	./bin/kvbench report --in $(PUBLIC_OUT) --md

# Build the cgo-only adapters too, so that path keeps compiling.
cgo:
	CGO_ENABLED=1 $(GO) build -tags cgo_engines ./...

clean:
	rm -rf bin dist results
