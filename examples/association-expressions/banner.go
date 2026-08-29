package main

// banner is the picture of what the two shapes do, printed before the run.
const banner = `
  association-expressions (both of BPMN's association expression shapes):

    order {total: 120, status: "new"}   rate {2}
         │                                  │
         └────────── transformation ────────┘      order.total * rate
                          │
                          ▼
                    charge.amount (240)
                          │
                    charge.note ──── assignment ───▶ order.status
                                     writes ONE FIELD, total is untouched

    A transformation REPLACES its target and may read several sources —
    which is what makes several sources legal. An assignment writes at a
    path inside its own association's target.

`
