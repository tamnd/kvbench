//go:build network_engines

package main

// Networked adapters are compiled in only under the network_engines build tag.
// They need a server binary on PATH at run time (the adapter launches it), so
// they stay out of the default build. redis, valkey and aki share the respnet
// launcher; dragonfly, garnet and kvrocks speak RESP but bring their own flags.
import (
	_ "github.com/tamnd/kvbench/adapters/aki"
	_ "github.com/tamnd/kvbench/adapters/dragonfly"
	_ "github.com/tamnd/kvbench/adapters/garnet"
	_ "github.com/tamnd/kvbench/adapters/kvredis"
	_ "github.com/tamnd/kvbench/adapters/kvrediscache"
	_ "github.com/tamnd/kvbench/adapters/kvrocks"
	_ "github.com/tamnd/kvbench/adapters/redis"
	_ "github.com/tamnd/kvbench/adapters/valkey"
)
