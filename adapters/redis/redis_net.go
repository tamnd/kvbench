//go:build network_engines

// Package redis adapts Redis in network mode. The adapter owns the server: it
// launches a private redis-server bound to a unix socket inside the cell's data
// directory, talks to it with the pure-Go go-redis client, and shuts it down on
// Close. No Docker and no shared port. redis-server must be on PATH; if it is
// not, Open returns an error and the harness marks the cell unsupported.
//
// Redis string keys are not stored in sorted order, so this adapter is
// unordered and the scan workloads skip it, the same as any hash-indexed store.
package redis

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/tamnd/kvbench/engine"
)

func init() { engine.Register("redis", func() engine.Engine { return &eng{} }) }

// sockSeq keeps unix socket paths unique and short. macOS caps a unix socket
// path at ~104 bytes, and the harness data dir lives under a long /var/folders
// temp path, so the socket cannot sit next to the data. It goes in /tmp instead.
var sockSeq atomic.Uint64

type eng struct {
	cmd  *exec.Cmd
	cli  *goredis.Client
	sock string
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "redis", Family: engine.FamilyHashLog, Mode: engine.ModeNetwork,
		Version: "server-on-path",
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: true, Durable: true,
			SingleFile: false, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{
			{Code: "default-durability", Note: "the default is AOF appendonly with appendfsync=everysec: the append log is fsynced about once a second, the Redis out-of-box durability default, not per command"},
			{Code: "network-hop", Note: "every op crosses a unix socket to a separate server process; the round-trip is in the number, which is the honest cost of a networked store"},
			{Code: "unordered", Note: "plain string keys have no sorted iteration, so the scan workloads skip redis"},
		},
	}
}

func (e *eng) Open(ctx context.Context, cfg engine.Config) error {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		return errors.New("redis-server not found on PATH")
	}
	e.sock = filepath.Join("/tmp", "kvbr-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(sockSeq.Add(1), 10)+".sock")
	appendonly := "yes"
	fsync := "everysec"
	switch cfg.Synchronous {
	case "OFF":
		appendonly = "no"
	case "FULL":
		fsync = "always"
	}
	args := []string{
		"--port", "0",
		"--unixsocket", e.sock,
		"--dir", cfg.Dir,
		"--save", "",
		"--appendonly", appendonly,
		"--appendfsync", fsync,
		"--daemonize", "no",
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	e.cmd = cmd

	cli := goredis.NewClient(&goredis.Options{Network: "unix", Addr: e.sock})
	// wait for the socket to accept and PING to return.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := cli.Ping(ctx).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return errors.New("redis-server did not come up in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.cli = cli
	return nil
}

func (e *eng) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	v, err := e.cli.Get(ctx, string(key)).Bytes()
	if err == goredis.Nil {
		return nil, false, nil
	}
	return v, err == nil, err
}

func (e *eng) Put(ctx context.Context, key, value []byte) error {
	return e.cli.Set(ctx, string(key), value, 0).Err()
}

func (e *eng) Delete(ctx context.Context, key []byte) error {
	return e.cli.Del(ctx, string(key)).Err()
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e, p: e.cli.Pipeline()} }

func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return nil, errors.New("redis string keyspace is unordered: no sorted scan")
}

func (e *eng) Flush(ctx context.Context) error { return e.cli.BgRewriteAOF(ctx).Err() }

// Stats leaves the RUM fields unknown; the harness walks the data dir for the
// on-disk size, which is where the AOF lives.
func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(ctx context.Context) error {
	if e.cli != nil {
		_ = e.cli.Shutdown(ctx).Err() // best effort; server may exit before replying
		_ = e.cli.Close()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_, _ = e.cmd.Process.Wait()
	}
	if e.sock != "" {
		_ = os.Remove(e.sock)
	}
	return nil
}

type batch struct {
	e *eng
	p goredis.Pipeliner
	n int
}

func (b *batch) Put(k, v []byte) {
	b.p.Set(context.Background(), string(k), append([]byte(nil), v...), 0)
	b.n++
}
func (b *batch) Delete(k []byte) { b.p.Del(context.Background(), string(k)); b.n++ }
func (b *batch) Len() int        { return b.n }
func (b *batch) Commit(ctx context.Context) error {
	_, err := b.p.Exec(ctx)
	b.n = 0
	return err
}
