package main

// banner is the picture of the process the example runs, printed first so
// the run log below reads against it.
const banner = `
  transaction-sub-process (a booking Transaction that cancels):
    start → (booking) → end
              ⚡ cancel-bnd → notify-customer
    booking = reserve-seat → charge-card → cancel-booking (Cancel End)
                ╳ release-seat  ╳ refund-card
    the Cancel End aborts: refund-card runs BEFORE release-seat, then
    control exits the Cancel boundary to notify-customer
    the booking states protocol="saga-v1"; the engine carries it, never
    reads it, and coordinates the abort itself (method=compensate)

`
