// engines wraps each Rust key/value store behind one small trait so the protocol
// loop in main.rs stays engine-blind. Each store maps the kvbench durability
// level (OFF / NORMAL / FULL) onto its own knob, documented at the call site.

use std::error::Error;
use std::path::Path;

pub enum BatchOp {
    Put(Vec<u8>, Vec<u8>),
    Del(Vec<u8>),
}

pub trait Kv: Send + Sync {
    fn get(&self, k: &[u8]) -> Option<Vec<u8>>;
    fn put(&self, k: &[u8], v: &[u8]);
    fn del(&self, k: &[u8]);
    fn scan(&self, start: &[u8], limit: u32) -> Vec<(Vec<u8>, Vec<u8>)>;
    fn flush(&self);
    fn batch(&self, ops: &[BatchOp]);
}

pub fn open(engine: &str, dir: &str, sync: &str) -> Result<Box<dyn Kv>, Box<dyn Error>> {
    match engine {
        "redb" => Ok(Box::new(Redb::open(dir, sync)?)),
        "sled" => Ok(Box::new(Sled::open(dir, sync)?)),
        "fjall" => Ok(Box::new(Fjall::open(dir, sync)?)),
        other => Err(format!("unknown engine {other}").into()),
    }
}

// ---- redb: a copy-on-write B+tree, the Rust analogue of LMDB/bbolt ----

use redb::{Database, Durability, TableDefinition};

const TABLE: TableDefinition<&[u8], &[u8]> = TableDefinition::new("kv");

struct Redb {
    db: Database,
    dur: Durability,
}

impl Redb {
    fn open(dir: &str, sync: &str) -> Result<Self, Box<dyn Error>> {
        let path = Path::new(dir).join("data.redb");
        let db = Database::create(path)?;
        // redb sets durability per write txn. None skips the fsync, Eventual lets
        // the OS flush, Immediate fsyncs the commit. That lines up with OFF /
        // NORMAL / FULL.
        let dur = match sync {
            "OFF" => Durability::None,
            "FULL" => Durability::Immediate,
            _ => Durability::Eventual,
        };
        Ok(Redb { db, dur })
    }
    fn write_txn(&self) -> redb::WriteTransaction {
        let mut tx = self.db.begin_write().unwrap();
        let _ = tx.set_durability(self.dur);
        tx
    }
}

impl Kv for Redb {
    fn get(&self, k: &[u8]) -> Option<Vec<u8>> {
        let rtx = self.db.begin_read().ok()?;
        let t = rtx.open_table(TABLE).ok()?;
        t.get(k).ok()?.map(|g| g.value().to_vec())
    }
    fn put(&self, k: &[u8], v: &[u8]) {
        let tx = self.write_txn();
        {
            let mut t = tx.open_table(TABLE).unwrap();
            t.insert(k, v).unwrap();
        }
        tx.commit().unwrap();
    }
    fn del(&self, k: &[u8]) {
        let tx = self.write_txn();
        {
            let mut t = tx.open_table(TABLE).unwrap();
            let _ = t.remove(k);
        }
        tx.commit().unwrap();
    }
    fn scan(&self, start: &[u8], limit: u32) -> Vec<(Vec<u8>, Vec<u8>)> {
        let mut out = Vec::new();
        let rtx = match self.db.begin_read() {
            Ok(r) => r,
            Err(_) => return out,
        };
        let t = match rtx.open_table(TABLE) {
            Ok(t) => t,
            Err(_) => return out,
        };
        if let Ok(range) = t.range(start..) {
            for entry in range {
                if let Ok((k, v)) = entry {
                    out.push((k.value().to_vec(), v.value().to_vec()));
                    if out.len() as u32 >= limit {
                        break;
                    }
                }
            }
        }
        out
    }
    fn flush(&self) {
        // a committed Immediate txn is already durable; force one to be safe.
        let mut tx = self.db.begin_write().unwrap();
        let _ = tx.set_durability(Durability::Immediate);
        tx.commit().unwrap();
    }
    fn batch(&self, ops: &[BatchOp]) {
        let tx = self.write_txn();
        {
            let mut t = tx.open_table(TABLE).unwrap();
            for op in ops {
                match op {
                    BatchOp::Put(k, v) => {
                        t.insert(k.as_slice(), v.as_slice()).unwrap();
                    }
                    BatchOp::Del(k) => {
                        let _ = t.remove(k.as_slice());
                    }
                }
            }
        }
        tx.commit().unwrap();
    }
}

// ---- sled: a lock-free log-structured B+tree ----

struct Sled {
    db: sled::Db,
    full: bool,
}

impl Sled {
    fn open(dir: &str, sync: &str) -> Result<Self, Box<dyn Error>> {
        let path = Path::new(dir).join("sled");
        // flush_every_ms is sled's background durability knob. OFF turns it off,
        // NORMAL leaves the periodic flush on, FULL also flushes after each write.
        let fems = match sync {
            "OFF" => None,
            _ => Some(1000u64),
        };
        let db = sled::Config::new().path(path).flush_every_ms(fems).open()?;
        Ok(Sled { db, full: sync == "FULL" })
    }
}

impl Kv for Sled {
    fn get(&self, k: &[u8]) -> Option<Vec<u8>> {
        self.db.get(k).ok().flatten().map(|v| v.to_vec())
    }
    fn put(&self, k: &[u8], v: &[u8]) {
        self.db.insert(k, v).unwrap();
        if self.full {
            self.db.flush().unwrap();
        }
    }
    fn del(&self, k: &[u8]) {
        let _ = self.db.remove(k);
        if self.full {
            self.db.flush().unwrap();
        }
    }
    fn scan(&self, start: &[u8], limit: u32) -> Vec<(Vec<u8>, Vec<u8>)> {
        let mut out = Vec::new();
        for entry in self.db.range(start.to_vec()..) {
            if let Ok((k, v)) = entry {
                out.push((k.to_vec(), v.to_vec()));
                if out.len() as u32 >= limit {
                    break;
                }
            }
        }
        out
    }
    fn flush(&self) {
        self.db.flush().unwrap();
    }
    fn batch(&self, ops: &[BatchOp]) {
        let mut b = sled::Batch::default();
        for op in ops {
            match op {
                BatchOp::Put(k, v) => b.insert(k.as_slice(), v.as_slice()),
                BatchOp::Del(k) => b.remove(k.as_slice()),
            }
        }
        self.db.apply_batch(b).unwrap();
        if self.full {
            self.db.flush().unwrap();
        }
    }
}

// ---- fjall: an LSM-tree with a write-ahead journal ----

use fjall::{Config, Keyspace, PartitionCreateOptions, PartitionHandle, PersistMode};

struct Fjall {
    ks: Keyspace,
    part: PartitionHandle,
    mode: Option<PersistMode>,
}

impl Fjall {
    fn open(dir: &str, sync: &str) -> Result<Self, Box<dyn Error>> {
        let path = Path::new(dir).join("fjall");
        let ks = Config::new(path).open()?;
        let part = ks.open_partition("kv", PartitionCreateOptions::default())?;
        // fjall buffers writes in a journal. NORMAL persists the buffer without an
        // fsync, FULL fsyncs the journal on every commit, OFF persists nothing.
        let mode = match sync {
            "OFF" => None,
            "FULL" => Some(PersistMode::SyncAll),
            _ => Some(PersistMode::Buffer),
        };
        Ok(Fjall { ks, part, mode })
    }
    fn persist(&self) {
        if let Some(m) = self.mode {
            let _ = self.ks.persist(m);
        }
    }
}

impl Kv for Fjall {
    fn get(&self, k: &[u8]) -> Option<Vec<u8>> {
        self.part.get(k).ok().flatten().map(|v| v.to_vec())
    }
    fn put(&self, k: &[u8], v: &[u8]) {
        self.part.insert(k, v).unwrap();
        self.persist();
    }
    fn del(&self, k: &[u8]) {
        let _ = self.part.remove(k);
        self.persist();
    }
    fn scan(&self, start: &[u8], limit: u32) -> Vec<(Vec<u8>, Vec<u8>)> {
        let mut out = Vec::new();
        for entry in self.part.range(start.to_vec()..) {
            if let Ok((k, v)) = entry {
                out.push((k.to_vec(), v.to_vec()));
                if out.len() as u32 >= limit {
                    break;
                }
            }
        }
        out
    }
    fn flush(&self) {
        let _ = self.ks.persist(PersistMode::SyncAll);
    }
    fn batch(&self, ops: &[BatchOp]) {
        let mut b = self.ks.batch();
        for op in ops {
            match op {
                BatchOp::Put(k, v) => b.insert(&self.part, k.as_slice(), v.as_slice()),
                BatchOp::Del(k) => b.remove(&self.part, k.as_slice()),
            }
        }
        b.commit().unwrap();
        self.persist();
    }
}
