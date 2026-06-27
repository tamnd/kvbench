//go:build subprocess_engines

// Package rustkv drives Rust key/value engines (redb, sled, fjall) in
// subprocess mode. It launches the kvbench-rs helper binary and speaks a
// compact length-prefixed binary protocol over its stdin/stdout.
//
// The engine instance is shared by all client goroutines, so the protocol is
// multiplexed: every request carries a u64 id, a single write mutex frames
// requests onto the pipe, and one reader goroutine fans responses back to the
// waiting caller by id. That keeps real concurrency instead of serializing every
// op behind one in-flight request. The helper runs a worker pool against the
// engine, so concurrent requests are served in parallel.
//
// kvbench-rs must be on PATH (build it from rust/ with `cargo build --release`
// and put the binary on PATH). If it is missing, Open errors and the harness
// marks the cell unsupported.
package rustkv

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/tamnd/kvbench/engine"
)

func register(name string) {
	engine.Register(name, func() engine.Engine { return &eng{name: name} })
}

func init() {
	register("redb")
	register("sled")
	register("fjall")
}

const (
	opPut   = 0x01
	opGet   = 0x02
	opDel   = 0x03
	opScan  = 0x04
	opFlush = 0x05
	opBatch = 0x06
)

type eng struct {
	name  string
	cmd   *exec.Cmd
	in    *bufio.Writer
	out   *bufio.Reader
	stdin io.WriteCloser

	wmu     sync.Mutex // serializes request framing onto the pipe
	nextID  uint64
	idmu    sync.Mutex
	wait    map[uint64]chan response
	readErr error
}

type response struct {
	status byte
	body   []byte
}

func (e *eng) Meta() engine.Meta {
	fam := engine.FamilyLSM
	ordered := true
	switch e.name {
	case "redb":
		fam = engine.FamilyCOWBTree
	case "sled":
		fam = engine.FamilyLSM
	case "fjall":
		fam = engine.FamilyLSM
	}
	return engine.Meta{
		Name: e.name, Family: fam, Mode: engine.ModeSubprocess,
		Version: "kvbench-rs",
		Caps: engine.Capabilities{
			Ordered: ordered, AtomicBatch: true, Durable: true, Transactions: e.name == "redb",
			SingleFile: e.name == "redb", PureNoCgo: false,
		},
		Asterisks: []engine.Asterisk{
			{Code: "subprocess-hop", Note: "every op crosses a pipe to a separate Rust process; the framing and round-trip cost is in the number, which is the honest cost of driving a non-Go engine this way"},
		},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	bin, err := exec.LookPath("kvbench-rs")
	if err != nil {
		return errors.New("kvbench-rs not found on PATH")
	}
	sync := cfg.Synchronous
	if sync == "" {
		sync = "NORMAL"
	}
	cmd := exec.Command(bin, "--engine", e.name, "--dir", cfg.Dir, "--sync", sync)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	e.cmd = cmd
	e.stdin = stdin
	e.in = bufio.NewWriterSize(stdin, 1<<16)
	e.out = bufio.NewReaderSize(stdout, 1<<16)
	e.wait = make(map[uint64]chan response)
	go e.reader()
	return nil
}

// reader pulls framed responses off the pipe and delivers them by id.
// Frame: id u64, status u8, blen u32, body[blen].
func (e *eng) reader() {
	var hdr [13]byte
	for {
		if _, err := io.ReadFull(e.out, hdr[:]); err != nil {
			e.failAll(err)
			return
		}
		id := binary.LittleEndian.Uint64(hdr[0:8])
		status := hdr[8]
		blen := binary.LittleEndian.Uint32(hdr[9:13])
		var body []byte
		if blen > 0 {
			body = make([]byte, blen)
			if _, err := io.ReadFull(e.out, body); err != nil {
				e.failAll(err)
				return
			}
		}
		e.idmu.Lock()
		ch := e.wait[id]
		delete(e.wait, id)
		e.idmu.Unlock()
		if ch != nil {
			ch <- response{status: status, body: body}
		}
	}
}

func (e *eng) failAll(err error) {
	e.idmu.Lock()
	e.readErr = err
	for id, ch := range e.wait {
		close(ch)
		delete(e.wait, id)
	}
	e.idmu.Unlock()
}

// call frames one request and blocks for its response. payload is the op-specific
// bytes that follow the [op, id] header.
func (e *eng) call(op byte, payload []byte) (response, error) {
	e.idmu.Lock()
	if e.readErr != nil {
		err := e.readErr
		e.idmu.Unlock()
		return response{}, err
	}
	id := e.nextID
	e.nextID++
	ch := make(chan response, 1)
	e.wait[id] = ch
	e.idmu.Unlock()

	var hdr [13]byte
	hdr[0] = op
	binary.LittleEndian.PutUint64(hdr[1:9], id)
	binary.LittleEndian.PutUint32(hdr[9:13], uint32(len(payload)))
	e.wmu.Lock()
	_, err := e.in.Write(hdr[:])
	if err == nil && len(payload) > 0 {
		_, err = e.in.Write(payload)
	}
	if err == nil {
		err = e.in.Flush()
	}
	e.wmu.Unlock()
	if err != nil {
		return response{}, err
	}
	r, ok := <-ch
	if !ok {
		return response{}, e.readErr
	}
	return r, nil
}

func (e *eng) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	p := make([]byte, 4+len(key))
	binary.LittleEndian.PutUint32(p, uint32(len(key)))
	copy(p[4:], key)
	r, err := e.call(opGet, p)
	if err != nil {
		return nil, false, err
	}
	if r.status == 0 {
		return nil, false, nil
	}
	return r.body, true, nil
}

func (e *eng) Put(_ context.Context, key, value []byte) error {
	p := make([]byte, 4+len(key)+4+len(value))
	binary.LittleEndian.PutUint32(p[0:], uint32(len(key)))
	copy(p[4:], key)
	off := 4 + len(key)
	binary.LittleEndian.PutUint32(p[off:], uint32(len(value)))
	copy(p[off+4:], value)
	r, err := e.call(opPut, p)
	if err != nil {
		return err
	}
	if r.status != 0 {
		return fmt.Errorf("put failed status=%d", r.status)
	}
	return nil
}

func (e *eng) Delete(_ context.Context, key []byte) error {
	p := make([]byte, 4+len(key))
	binary.LittleEndian.PutUint32(p, uint32(len(key)))
	copy(p[4:], key)
	_, err := e.call(opDel, p)
	return err
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(_ context.Context, start []byte) (engine.Iterator, error) {
	// The protocol materializes the scan into one response, so it needs a hard
	// cap. The driver never reads more than ScanLen (at most 100) entries per
	// scan, so a few hundred is plenty and keeps the response small. A larger cap
	// would copy the whole keyspace across the pipe for every scan op.
	const cap = 512
	p := make([]byte, 4+len(start)+4)
	binary.LittleEndian.PutUint32(p, uint32(len(start)))
	copy(p[4:], start)
	binary.LittleEndian.PutUint32(p[4+len(start):], cap)
	r, err := e.call(opScan, p)
	if err != nil {
		return nil, err
	}
	return &iter{body: r.body}, nil
}

func (e *eng) Flush(_ context.Context) error {
	_, err := e.call(opFlush, nil)
	return err
}

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_, _ = e.cmd.Process.Wait()
	}
	return nil
}

type batch struct {
	e   *eng
	buf []byte
	n   int
}

// batch payload: count u32, then per op: kind u8, klen u32, k, [vlen u32, v].
func (b *batch) Put(k, v []byte) {
	rec := make([]byte, 1+4+len(k)+4+len(v))
	rec[0] = opPut
	binary.LittleEndian.PutUint32(rec[1:], uint32(len(k)))
	copy(rec[5:], k)
	off := 5 + len(k)
	binary.LittleEndian.PutUint32(rec[off:], uint32(len(v)))
	copy(rec[off+4:], v)
	b.buf = append(b.buf, rec...)
	b.n++
}
func (b *batch) Delete(k []byte) {
	rec := make([]byte, 1+4+len(k))
	rec[0] = opDel
	binary.LittleEndian.PutUint32(rec[1:], uint32(len(k)))
	copy(rec[5:], k)
	b.buf = append(b.buf, rec...)
	b.n++
}
func (b *batch) Len() int { return b.n }
func (b *batch) Commit(_ context.Context) error {
	p := make([]byte, 4+len(b.buf))
	binary.LittleEndian.PutUint32(p, uint32(b.n))
	copy(p[4:], b.buf)
	r, err := b.e.call(opBatch, p)
	b.buf = nil
	b.n = 0
	if err != nil {
		return err
	}
	if r.status != 0 {
		return fmt.Errorf("batch failed status=%d", r.status)
	}
	return nil
}

// iter walks a materialized scan response: repeated klen u32, k, vlen u32, v.
type iter struct {
	body []byte
	off  int
	k, v []byte
}

func (i *iter) Next() bool {
	if i.off+4 > len(i.body) {
		return false
	}
	kl := int(binary.LittleEndian.Uint32(i.body[i.off:]))
	i.off += 4
	if i.off+kl+4 > len(i.body) {
		return false
	}
	i.k = i.body[i.off : i.off+kl]
	i.off += kl
	vl := int(binary.LittleEndian.Uint32(i.body[i.off:]))
	i.off += 4
	if i.off+vl > len(i.body) {
		return false
	}
	i.v = i.body[i.off : i.off+vl]
	i.off += vl
	return true
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return nil }
func (i *iter) Close() error  { return nil }
