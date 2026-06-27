//go:build network_engines

package main

// Networked adapters are compiled in only under the network_engines build tag.
// They need a server binary on PATH at run time (the adapter launches it), so
// they stay out of the default build.
import _ "github.com/tamnd/kvbench/adapters/redis"
