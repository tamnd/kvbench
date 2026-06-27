# kvbench is pure Go with no cgo in the default build, so every target here runs
# with CGO_ENABLED=0. The cgo-only adapters (LMDB) sit behind the cgo_engines
# build tag and are built by the `cgo` target.

GO ?= go
PKG ?= ./...

.PHONY: build test race lint vet fmt fmt-check tidy vuln bench smoke cgo clean

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

# Build the cgo-only adapters too, so that path keeps compiling.
cgo:
	CGO_ENABLED=1 $(GO) build -tags cgo_engines ./...

clean:
	rm -rf bin dist results
