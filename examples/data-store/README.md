# data-store

The engine-global **Data Store** (BPMN §10.4.1, ADR-030 / SRD-068): data that
**outlives a Process instance** and is **shared across instances** within the
running engine.

Two processes share one in-memory store, registered on the engine with
`thresher.WithDataStore("shared", memstore.New())`:

- a **writer** instance stores `42` into a `DataStoreReference` named `counter`
  (a `DataOutputAssociation`, Node → DataStore);
- a separate **reader** instance reads it back through a reference to the same
  store (a `DataInputAssociation`, DataStore → Node).

Unlike a `DataObject` (per-instance, scope-resident — see
[`examples/process-data`](../process-data/)), a `DataStoreReference`'s value is
engine-global, so the reader sees the writer's value even though it runs in a
different instance.

```
go run .
```

Expected:

```
✓ writer instance stored 42 in DataStore "shared" key "counter"
✓ reader instance read 42 from the shared DataStore
```
