# standard-loop

Demonstrates a BPMN **Standard Loop** (§13.3.6, SRD-054): an activity marked
`WithLoop` re-runs while its `loopCondition` holds.

```
start → work [loopCounter < 3] → end
```

`work` is a Service Task carrying a post-tested (`do…while`) Standard Loop. Each
pass reads the engine-published 0-based `loopCounter` and prints it; the loop
stops when `loopCounter < 3` is false, so the body runs three times
(`loopCounter` 0, 1, 2).

The same marker works on a Sub-Process or Call Activity — a composite re-opens
its child scope per iteration — and `WithTestBefore()` / `WithLoopMaximum(n)`
select a pre-tested loop and cap the count.

## Run

```bash
go run .
```
