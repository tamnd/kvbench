//go:build network_engines

// Package respnet is the shared launcher behind the RESP network adapters:
// redis, valkey, dragonfly and aki. They are all the same shape to the harness:
// a separate server process speaking the Redis wire protocol, reached over a
// private unix socket with the pure-Go go-redis client, owned and torn down by
// the adapter. The only differences between them are the binary name, the
// command-line flags each spelling wants, and how each one maps the durability
// contract, so those three are the per-engine Spec and everything else lives
// here once.
//
// Keeping the plumbing in one place is also a fairness point: every RESP engine
// crosses the identical socket with the identical client, so a difference in the
// number is the server, not the adapter.
package respnet

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
	"github.com/tamnd/kvbench/cpuset"
	"github.com/tamnd/kvbench/engine"
)

// Spec is the per-engine part of a RESP adapter. ArgsFn returns the full server
// argv given the cell config and the unix socket path the adapter picked, so an
// engine that spells durability or the data path its own way handles it there.
// FlushFn is the engine's "make it durable now" call, used by the harness Flush
// step; leave it nil for an engine with no such command.
type Spec struct {
	Name    string
	Version string
	Binary  string
	// Class is the leaderboard division the server belongs to. The in-memory
	// servers (redis, valkey, dragonfly) are ClassRedisMemory; the persistent
	// ones backed by an on-disk store (aki, kv-redis) are ClassRedisPersistent.
	// Empty falls back to ClassRedisMemory through engine.ClassOf.
	Class     engine.Class
	ArgsFn    func(cfg engine.Config, sock string) []string
	FlushFn   func(ctx context.Context, cli *goredis.Client) error
	Asterisks []engine.Asterisk
}

// New returns an engine.Engine driving the server described by spec. Register it
// from the concrete adapter package, for example
//
//	engine.Register("valkey", func() engine.Engine { return respnet.New(valkeySpec) })
func New(spec Spec) engine.Engine { return &eng{spec: spec} }

// RedisDialectArgs builds the server argv for a redis-flag-dialect server. Redis,
// valkey and aki all accept the same spelling: --port, --unixsocket, --dir,
// --save, --appendonly and --appendfsync. The network class is an everysec
// comparison, so the AOF stays on with the once-a-second fsync that is every one
// of these servers' shipped default. OFF turns the AOF off so the raw write path
// shows with no per-commit sync. FULL never reaches here: the harness skips the
// per-commit regime for networked engines, since appendfsync always over a socket
// is a mode nobody deploys. TCP is disabled with --port 0 so only the private unix
// socket is open.
func RedisDialectArgs(cfg engine.Config, sock string) []string {
	appendonly := "yes"
	if cfg.Synchronous == "OFF" {
		appendonly = "no"
	}
	return []string{
		"--port", "0",
		"--unixsocket", sock,
		"--dir", cfg.Dir,
		"--save", "",
		"--appendonly", appendonly,
		"--appendfsync", "everysec",
	}
}

// FlushAOF asks a redis-dialect server to rewrite its append-only file, the
// harness's "make it durable now" hook for redis and valkey.
func FlushAOF(ctx context.Context, cli *goredis.Client) error {
	return cli.BgRewriteAOF(ctx).Err()
}

// sockSeq keeps unix socket paths unique and short. macOS caps a unix socket
// path at ~104 bytes, and the harness data dir lives under a long /var/folders
// temp path, so the socket cannot sit next to the data. It goes in /tmp instead.
var sockSeq atomic.Uint64

type eng struct {
	spec Spec
	cmd  *exec.Cmd
	cli  *goredis.Client
	sock string
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: e.spec.Name, Family: engine.FamilyHashLog, Mode: engine.ModeNetwork,
		Class:   e.spec.Class,
		Version: e.spec.Version,
		Caps: engine.Capabilities{
			Ordered: false, AtomicBatch: true, Durable: true,
			SingleFile: false, PureNoCgo: true,
		},
		Asterisks: e.spec.Asterisks,
	}
}

func (e *eng) Open(ctx context.Context, cfg engine.Config) error {
	bin, err := exec.LookPath(e.spec.Binary)
	if err != nil {
		return errors.New(e.spec.Binary + " not found on PATH")
	}
	e.sock = filepath.Join("/tmp", "kvbr-"+e.spec.Name+"-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(sockSeq.Add(1), 10)+".sock")
	// When --cpu-split is on the harness pins itself (and so the go-redis client)
	// to one core set and hands the server a disjoint one, so the two never fight
	// for a core. ServerWrap folds the launch under taskset for that server set; it
	// is a no-op when the list is empty or taskset is absent.
	launchBin, launchArgs := cpuset.ServerWrap(cfg.ServerCPUList, bin, e.spec.ArgsFn(cfg, e.sock))
	cmd := exec.Command(launchBin, launchArgs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	e.cmd = cmd

	cli := goredis.NewClient(&goredis.Options{Network: "unix", Addr: e.sock})
	// Wait for the socket to accept and PING to return. A slow server (dragonfly
	// opens a snapshot file and warms its shards) can take a couple of seconds.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := cli.Ping(ctx).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return errors.New(e.spec.Name + " server did not come up in time")
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

func (e *eng) NewBatch() engine.Batch { return &batch{p: e.cli.Pipeline()} }

func (e *eng) Scan(_ context.Context, _ []byte) (engine.Iterator, error) {
	return nil, errors.New(e.spec.Name + " string keyspace is unordered: no sorted scan")
}

func (e *eng) Flush(ctx context.Context) error {
	if e.spec.FlushFn == nil {
		return nil
	}
	return e.spec.FlushFn(ctx, e.cli)
}

// Stats leaves the RUM fields unknown; the harness walks the data dir for the
// on-disk size, which is where the server's append log or snapshot lives.
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
