# restart-recovery

Demonstrates **instance checkpoints and restart recovery** (ADR-033 /
SRD-070): one shared repository, two engines, one crash.

```
engine-1: start → park on timer → checkpoint → 💥 abandoned
engine-2: claim the expired lease → restore at the RECORDED deadline
          → the timer fires → complete
```

What the trace shows:

- **Checkpoints at lifecycle transitions.** With an explicitly
  configured repository (`thresher.WithRepository`), every instance
  writes a consistent-cut checkpoint at its observable transitions —
  activation, node completion, wait parks, the terminal. The
  zero-config engine stays volatile (no repository configured = no
  overhead).
- **The crash is abandonment, not shutdown.** A graceful stop writes a
  terminal record; a crash leaves the record `Active` with an expiring
  **ownership lease** (`WithLeaseTTL`). Recovery lists only claimable
  records — non-terminal with expired leases.
- **Recovery re-enters the node.** Engine-2 claims the record under a
  higher lease incarnation, re-clones the **pinned process version**
  and respawns the parked track at its node. The timer re-arms at the
  **recorded absolute deadline** — a Duration never restarts, and an
  overdue deadline fires once, immediately.
- **Effects are at-least-once, state is exactly-once.** The zombie
  engine-1 still fires its in-memory copy (both `[engine-…]` lines
  print), but its checkpoint saves are **CAS-fenced** by the record
  version + lease incarnation — only the recovering engine's state
  survives, visible in the final owner.
- **Stable element ids are the deployment-parity contract.** Every
  node carries `foundation.WithID(...)`: the recovering engine resolves
  the checkpoint's recorded node ids against ITS registration of the
  same process version — two engines building the model without pinned
  ids would mint different ones.

## Run

```bash
cd examples/restart-recovery && go run .
```

Expected output: the park + abandonment lines, engine-2's recovery,
both engines' timer effects, and the closing `✓` naming `engine-2` as
the record's final owner.
