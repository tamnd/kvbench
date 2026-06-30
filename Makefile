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

# A fast end-to-end check that the harness still runs: a tiny sweep over a few
# real engines, durability off.
smoke: bin/kvbench
	./bin/kvbench run \
		--engines kv,bbolt,pebble,badger \
		--workloads readrandom,fillrandom \
		--durability OFF --conc 1 --cardinality 5000 --ops 10000 --reps 1 \
		--out results/smoke

# The portable public profile: the same matrix every host runs, in a single
# durability mode shared by every engine so the numbers are directly comparable.
# It is two passes (a raw read/write board with the disk flush off, then a small
# durable pass with a flush on every commit), both deterministic from a fixed
# seed, so the only variable left is the hardware. The fairness model is at
# https://kvbench.tamnd.com/methodology/ . The runner is scripts/bench-profile.sh;
# override OUT= to pick a results dir.
PUBLIC_OUT ?= results/public
bench-public: bin/kvbench
	scripts/bench-profile.sh ./bin/kvbench $(PUBLIC_OUT)
	./bin/kvbench report --in $(PUBLIC_OUT) --md

# Build the cgo-only adapters too, so that path keeps compiling.
cgo:
	CGO_ENABLED=1 $(GO) build -tags cgo_engines ./...

clean:
	rm -rf bin dist results
