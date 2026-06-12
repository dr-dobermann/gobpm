# process-data

Demonstrates the process data model (ADR-010 / SRD-007): a process property
lives in the instance's container scope; two parallel branches read it
through their own execution frames; each branch produces its result through
its frame, and the results reach the bound DataObjects at frame commit.

```
start ─> split ─┬─> greet-a ─> end-a    (result-a DataObject)
                └─> greet-b ─> end-b    (result-b DataObject)
```

The data path per branch: `user_name` property → container scope → the
frame's container walk → the operation's input message → the Go functor →
the frame (node-produced put) → the output instance → the output
association → the DataObject.

## Run

```sh
go run .
```

Expected: both greeters print their produced greeting, both DataObjects
carry their branch's result, then `data-demo completed`.
