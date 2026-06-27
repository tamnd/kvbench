// Package sqlite adapts SQLite as a key/value store via the pure-Go
// modernc.org/sqlite driver (no cgo). Schema: one table (k BLOB PRIMARY KEY,
// v BLOB) in WAL mode — the canonical "SQLite as a KV store" baseline and the
// operational-model twin kv targets.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/tamnd/kvbench/engine"
	_ "modernc.org/sqlite"
)

func init() { engine.Register("sqlite", func() engine.Engine { return &eng{} }) }

type eng struct {
	db *sql.DB
}

func (e *eng) Meta() engine.Meta {
	return engine.Meta{
		Name: "sqlite", Family: engine.FamilyBTree, Mode: engine.ModeInProc,
		Version: "modernc-pure-go",
		Caps: engine.Capabilities{
			Ordered: true, AtomicBatch: true, Durable: true, Transactions: true,
			OnlineBackup: true, SingleFile: true, PureNoCgo: true,
		},
		Asterisks: []engine.Asterisk{{Code: "sql-overhead", Note: "KV ops go through SQL prepared statements; reflects SQLite-as-KV, not raw btree"}},
	}
}

func (e *eng) Open(_ context.Context, cfg engine.Config) error {
	sync := "NORMAL"
	switch cfg.Synchronous {
	case "OFF":
		sync = "OFF"
	case "FULL":
		sync = "FULL"
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(%s)&_pragma=busy_timeout(5000)",
		filepath.Join(cfg.Dir, "data.sqlite"), sync)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1) // single connection keeps WAL writer semantics simple
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (k BLOB PRIMARY KEY, v BLOB) WITHOUT ROWID`); err != nil {
		_ = db.Close()
		return err
	}
	e.db = db
	return nil
}

func (e *eng) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	var v []byte
	err := e.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	return v, err == nil, err
}

func (e *eng) Put(ctx context.Context, key, value []byte) error {
	_, err := e.db.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, value)
	return err
}

func (e *eng) Delete(ctx context.Context, key []byte) error {
	_, err := e.db.ExecContext(ctx, `DELETE FROM kv WHERE k=?`, key)
	return err
}

func (e *eng) NewBatch() engine.Batch { return &batch{e: e} }

func (e *eng) Scan(ctx context.Context, start []byte) (engine.Iterator, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT k,v FROM kv WHERE k>=? ORDER BY k`, start)
	if err != nil {
		return nil, err
	}
	return &iter{rows: rows}, nil
}

func (e *eng) Flush(_ context.Context) error {
	_, _ = e.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

func (e *eng) Stats(_ context.Context) (engine.Stats, error) { return engine.UnknownStats(), nil }

func (e *eng) Close(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

type batch struct {
	e   *eng
	ops []op
}
type op struct {
	del  bool
	k, v []byte
}

func (b *batch) Put(k, v []byte) {
	b.ops = append(b.ops, op{k: append([]byte(nil), k...), v: append([]byte(nil), v...)})
}
func (b *batch) Delete(k []byte) { b.ops = append(b.ops, op{del: true, k: append([]byte(nil), k...)}) }
func (b *batch) Len() int        { return len(b.ops) }
func (b *batch) Commit(ctx context.Context) error {
	tx, err := b.e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	put, err := tx.PrepareContext(ctx, `INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	del, err := tx.PrepareContext(ctx, `DELETE FROM kv WHERE k=?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, o := range b.ops {
		if o.del {
			_, err = del.ExecContext(ctx, o.k)
		} else {
			_, err = put.ExecContext(ctx, o.k, o.v)
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	b.ops = nil
	return tx.Commit()
}

type iter struct {
	rows *sql.Rows
	k, v []byte
}

func (i *iter) Next() bool {
	if !i.rows.Next() {
		return false
	}
	i.k, i.v = nil, nil
	return i.rows.Scan(&i.k, &i.v) == nil
}
func (i *iter) Key() []byte   { return i.k }
func (i *iter) Value() []byte { return i.v }
func (i *iter) Err() error    { return i.rows.Err() }
func (i *iter) Close() error  { return i.rows.Close() }
