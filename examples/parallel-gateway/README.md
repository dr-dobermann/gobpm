# parallel-gateway

Demonstrates the BPMN Parallel (AND) gateway (SRD-005): a diverging gateway
forks every outgoing branch, the branches run concurrently, and a converging
gateway synchronizes them — the process continues only once every branch has
arrived.

```
start ─> split ─┬─> worker-a ─┬─> join ─> end
                └─> worker-b ─┘
```

Each worker is a `ServiceTask` whose operation prints when it runs, so the
output shows both branches executing before the join lets a single token reach
the End.

## Run

```sh
go run .
```

Expected: both `worker-a` and `worker-b` print, then `parallel-demo completed`.
