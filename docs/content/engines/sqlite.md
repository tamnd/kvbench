---
title: "sqlite"
linkTitle: "sqlite"
description: "sqlite (the pure-Go modernc build) is a full SQL database used as a key/value store. Slowest at raw key/value ops, but it brings real SQL and group-commit durability."
weight: 80
---

**Shape:** B-tree, full SQL, pure Go (modernc build)
**Repository:** [modernc.org/sqlite](https://modernc.org/sqlite)

sqlite is not really a key/value store, it is a full relational database, measured here through a two-column table used as one.
That framing explains both ends of its results: on raw `get` and `put` it is the slowest engine here, because it is paying for a SQL layer the others do not have, and on durable writes it is near the top, because that same mature engine practices group commit.

This is the pure-Go modernc build, so it adds no cgo and runs anywhere Go runs.

## Best at

- **Durable writes.** 2,152 durable writes/sec, second only to badger through the same group-commit trick. See [durable writes](/scenarios/durable-writes/).
- **Actual SQL.** Joins, indexes, transactions, schemas, the whole relational toolkit, if your data wants more than key/value.

## Watch out for

- **Slow raw key/value ops.** 52,000 reads/sec and 7,500 fresh writes/sec, the slowest here by a wide margin. The SQL machinery is overhead you do not want if all you need is `get` and `put`.
- **Space.** 4.50x the data, page and index overhead.

## Reach for it when

Your data is relational, or will be: you want queries, joins, and a schema, not just keyed blobs.
Or when you need durable writes under concurrency and value a battle-tested engine.
If you genuinely only need key/value, every other engine here is faster; pick one of them and keep sqlite for when the data grows relationships.
