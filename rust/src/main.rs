// kvbench-rs drives a Rust key/value engine for kvbench's subprocess mode.
//
// It reads length-prefixed binary requests on stdin and writes length-prefixed
// responses on stdout. Requests carry a u64 id so the Go side can multiplex many
// in-flight ops over the one pipe; this helper runs a small worker pool so those
// ops are served concurrently rather than one at a time.
//
// Request frame:  op u8, id u64 LE, plen u32 LE, payload[plen]
// Response frame: id u64 LE, status u8, blen u32 LE, body[blen]
//
// Ops and payloads:
//   PUT   0x01  klen u32, k, vlen u32, v            -> status 0 ok
//   GET   0x02  klen u32, k                          -> status 1 + value, or status 0
//   DEL   0x03  klen u32, k                          -> status 0 ok
//   SCAN  0x04  klen u32, start, limit u32           -> body = klen u32,k,vlen u32,v ...
//   FLUSH 0x05  (no payload)                         -> status 0 ok
//   BATCH 0x06  count u32, [kind u8,klen,k,(vlen,v)] -> status 0 ok

use std::io::{self, BufReader, BufWriter, Read, Write};
use std::sync::{Arc, Mutex};
use std::thread;

mod engines;
use engines::Kv;

const OP_PUT: u8 = 0x01;
const OP_GET: u8 = 0x02;
const OP_DEL: u8 = 0x03;
const OP_SCAN: u8 = 0x04;
const OP_FLUSH: u8 = 0x05;
const OP_BATCH: u8 = 0x06;

struct Request {
    op: u8,
    id: u64,
    payload: Vec<u8>,
}

fn main() {
    let mut engine = String::from("redb");
    let mut dir = String::from(".");
    let mut sync = String::from("NORMAL");
    let args: Vec<String> = std::env::args().collect();
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--engine" => { engine = args[i + 1].clone(); i += 2; }
            "--dir" => { dir = args[i + 1].clone(); i += 2; }
            "--sync" => { sync = args[i + 1].clone(); i += 2; }
            _ => { i += 1; }
        }
    }

    let kv: Arc<dyn Kv> = match engines::open(&engine, &dir, &sync) {
        Ok(k) => Arc::from(k),
        Err(e) => {
            eprintln!("kvbench-rs: open {engine} failed: {e}");
            std::process::exit(2);
        }
    };

    // One thread reads requests off stdin and feeds a shared queue; a pool of
    // workers pulls from it and writes responses back under a shared lock.
    let (tx, rx) = std::sync::mpsc::channel::<Request>();
    let rx = Arc::new(Mutex::new(rx));
    let out = Arc::new(Mutex::new(BufWriter::new(io::stdout())));

    let workers = 8;
    let mut handles = Vec::new();
    for _ in 0..workers {
        let rx = Arc::clone(&rx);
        let out = Arc::clone(&out);
        let kv = Arc::clone(&kv);
        handles.push(thread::spawn(move || loop {
            let req = {
                let guard = rx.lock().unwrap();
                guard.recv()
            };
            let req = match req {
                Ok(r) => r,
                Err(_) => break, // channel closed: stdin ended
            };
            let (status, body) = handle(&*kv, req.op, &req.payload);
            write_response(&out, req.id, status, &body);
        }));
    }

    // reader loop
    let mut r = BufReader::new(io::stdin());
    loop {
        let mut hdr = [0u8; 13];
        if r.read_exact(&mut hdr).is_err() {
            break;
        }
        let op = hdr[0];
        let id = u64::from_le_bytes(hdr[1..9].try_into().unwrap());
        let plen = u32::from_le_bytes(hdr[9..13].try_into().unwrap()) as usize;
        let mut payload = vec![0u8; plen];
        if plen > 0 && r.read_exact(&mut payload).is_err() {
            break;
        }
        if tx.send(Request { op, id, payload }).is_err() {
            break;
        }
    }
    drop(tx); // close the queue so workers exit
    for h in handles {
        let _ = h.join();
    }
}

fn handle(kv: &dyn Kv, op: u8, p: &[u8]) -> (u8, Vec<u8>) {
    match op {
        OP_GET => {
            let (k, _) = read_bytes(p, 0);
            match kv.get(k) {
                Some(v) => (1, v),
                None => (0, Vec::new()),
            }
        }
        OP_PUT => {
            let (k, off) = read_bytes(p, 0);
            let (v, _) = read_bytes(p, off);
            kv.put(k, v);
            (0, Vec::new())
        }
        OP_DEL => {
            let (k, _) = read_bytes(p, 0);
            kv.del(k);
            (0, Vec::new())
        }
        OP_SCAN => {
            let (start, off) = read_bytes(p, 0);
            let limit = u32::from_le_bytes(p[off..off + 4].try_into().unwrap());
            let rows = kv.scan(start, limit);
            let mut body = Vec::new();
            for (k, v) in rows {
                body.extend_from_slice(&(k.len() as u32).to_le_bytes());
                body.extend_from_slice(&k);
                body.extend_from_slice(&(v.len() as u32).to_le_bytes());
                body.extend_from_slice(&v);
            }
            (0, body)
        }
        OP_FLUSH => {
            kv.flush();
            (0, Vec::new())
        }
        OP_BATCH => {
            let mut off = 0usize;
            let count = u32::from_le_bytes(p[off..off + 4].try_into().unwrap());
            off += 4;
            let mut ops: Vec<engines::BatchOp> = Vec::with_capacity(count as usize);
            for _ in 0..count {
                let kind = p[off];
                off += 1;
                let (k, no) = read_bytes(p, off);
                off = no;
                if kind == OP_PUT {
                    let (v, no2) = read_bytes(p, off);
                    off = no2;
                    ops.push(engines::BatchOp::Put(k.to_vec(), v.to_vec()));
                } else {
                    ops.push(engines::BatchOp::Del(k.to_vec()));
                }
            }
            kv.batch(&ops);
            (0, Vec::new())
        }
        _ => (255, Vec::new()),
    }
}

// read_bytes reads a u32 length-prefixed slice at offset and returns it with the
// offset just past it.
fn read_bytes(p: &[u8], off: usize) -> (&[u8], usize) {
    let n = u32::from_le_bytes(p[off..off + 4].try_into().unwrap()) as usize;
    let start = off + 4;
    (&p[start..start + n], start + n)
}

fn write_response(out: &Arc<Mutex<BufWriter<io::Stdout>>>, id: u64, status: u8, body: &[u8]) {
    let mut w = out.lock().unwrap();
    let _ = w.write_all(&id.to_le_bytes());
    let _ = w.write_all(&[status]);
    let _ = w.write_all(&(body.len() as u32).to_le_bytes());
    if !body.is_empty() {
        let _ = w.write_all(body);
    }
    let _ = w.flush();
}
