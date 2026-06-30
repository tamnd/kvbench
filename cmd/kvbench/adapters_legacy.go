//go:build network_engines && legacy_engines

package main

// Legacy adapters are double-gated: they need network_engines for the RESP
// launcher and legacy_engines to opt in, so a normal network build leaves them
// out. They are kept as labeled historical rails, never as headline competitors.
// Build with `-tags "network_engines legacy_engines"` to include them.
import (
	_ "github.com/tamnd/kvbench/adapters/legacykeydb"
)
