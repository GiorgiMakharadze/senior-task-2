# Answers

## Q1: Where should the refund HTTP call happen?

**Answer: D — As a separate usecase triggered by `SubscriptionCancelledEvent`.**

### How this is implemented in the code

The cancel usecase commits two mutations atomically in a single `Plan`:
1. A subscription state update (status → CANCELLED, refundCents, cancelledAt)
2. An outbox insert containing the `SubscriptionCancelledEvent` with the refund amount

Both mutations are applied through one `Committer.Apply(ctx, plan)` call, so they either both succeed or both fail. A separate refund worker (not implemented here — it is outside the subscription bounded context) would read from the outbox table and call the billing refund API.

**Concurrent cancel safety:** The cancel mutation uses a conditional UPDATE (`WHERE id = ? AND status != 'cancelled'`) with `ExpectRowsAffected`, so if two concurrent cancels race past the in-memory check, only the first commit succeeds. The second produces zero affected rows, the committer returns `ErrStaleWrite`, and the usecase translates this to `ErrSubscriptionAlreadyCancelled` — no duplicate outbox event is produced.

### Trade-off analysis

**Option B (inside domain `Cancel()` method):** Immediately disqualified. The domain must remain pure — no I/O, no HTTP, no side effects beyond state mutation and event raising. Putting an HTTP call inside the aggregate breaks domain purity, makes the aggregate untestable without a network, and violates the fundamental boundary between domain logic and infrastructure.

**Option A (inside the Cancel usecase):** Simple and synchronous. The usecase calls cancel on the aggregate, commits the state change, then calls the refund API. The problem: if the refund API fails after the subscription is already committed as cancelled, you have a split-state situation. Retrying the whole usecase is unsafe because the subscription is already cancelled. You end up needing special compensation logic inside the usecase anyway, which defeats the simplicity argument.

**Option C (in service layer after `committer.Apply()` succeeds):** Better than A because it ensures the state change is durable before attempting the refund. But it is still synchronous — the caller blocks on the refund API, and if it fails, you need inline retry logic or must accept that the refund is silently lost. The usecase grows more complex with retry/error handling for an external call that is not part of its core responsibility.

**Option D (separate usecase triggered by `SubscriptionCancelledEvent`):** The strongest architectural answer. Cancellation and refund are separate business concerns with different failure modes:

- Cancellation is a synchronous state transition that must succeed or fail atomically.
- Refund is an asynchronous side effect that may fail, needs retries, and can be eventually consistent.

By committing the `SubscriptionCancelledEvent` into an outbox table within the same transaction as the state change, a separate refund handler can:
- Pick up the event asynchronously
- Retry independently with its own backoff strategy
- Be idempotent using the event/subscription ID as a deduplication key
- Not block the cancellation response
- Be monitored and alerted separately

**The cost:** Eventual consistency — the refund is not immediate. You need an outbox table, a polling or CDC consumer, and idempotency handling. For a production subscription system handling real money, this additional complexity is justified and expected.

---

## Q2: If Cancel() works but the refund API is down

### What should happen to the subscription status?

The subscription should remain **CANCELLED**. Cancellation is the business fact — the customer requested cancellation, and the system honored it. The refund is a financial consequence of that decision, not a precondition. Rolling back the cancellation because a payment API is temporarily down would create a worse user experience and a confusing state.

In our implementation, the subscription status and the outbox event are committed atomically. The outbox row durably records that a refund of a specific amount is owed. The refund API being down does not affect the cancellation itself.

### Should we retry? When? How many times?

Yes, retry the refund. Strategy:

1. The outbox row contains the `SubscriptionCancelledEvent` with `refundCents` and subscription ID.
2. A refund worker reads unprocessed outbox rows and attempts the refund API call.
3. Use exponential backoff with jitter: e.g., 1s, 2s, 4s base delays with random jitter to avoid thundering herd.
4. Retry up to 3 times (or a configured limit) before escalating.
5. Each retry attempt must be idempotent — use the subscription ID as an idempotency key with the billing API.

### What if refund fails after 3 retries?

1. Mark the outbox row as `REFUND_FAILED` or move it to a dead-letter table.
2. **Alert operations** — a failed refund is a financial discrepancy that requires human attention.
3. Record the failure with full context: subscription ID, refund amount, error details, attempt count, timestamps.
4. Support **manual retry** — an operator should be able to trigger a re-attempt after the billing API recovers.
5. Consider a **reconciliation job** that periodically scans for subscriptions in CANCELLED status with no corresponding successful refund, as a safety net.

Do not silently drop the refund. Do not automatically charge the customer again. The system should make failed refunds visible and recoverable.

---

## Q3: Two problems with the refund calculation

### Problem 1: Why is `time.Since()` wrong?

```go
daysUsed := time.Since(sub.StartDate).Hours() / 24
```

`time.Since()` calls `time.Now()` internally, creating a hidden wall-clock dependency:

1. **Non-deterministic:** The result changes every nanosecond. Tests cannot assert a specific refund amount because the elapsed time differs between test runs.
2. **Non-reproducible:** Debugging, replaying, or auditing the calculation is impossible — you cannot reconstruct what `time.Now()` returned.
3. **Incorrect for backfill/replay:** If you reprocess historical events or backfill data, `time.Since()` computes elapsed time from the current moment, not from the business-relevant point in time.
4. **Timezone fragility:** Depending on server timezone configuration, `time.Now()` may produce different results on different hosts.

The fix is to inject a `Clock` interface and pass the resulting `now` explicitly into the domain method.

### Problem 2: Why is float math dangerous?

```go
refundAmount := sub.Price * (30 - daysUsed) / 30
```

IEEE 754 floating-point cannot represent most decimal fractions exactly. For example:
- `29.99` is not exactly representable in float64
- `29.99 * 20 / 30` may produce `19.993333...` instead of `19.99333...`
- Accumulated rounding errors across thousands of transactions create real financial discrepancies

Additionally, `daysUsed` is a float from the hours division, introducing further imprecision into the multiplication chain.

### Corrected implementation using int64 cents and injected clock

```go
type Clock interface {
    Now() time.Time
}

func (s *Subscription) Cancel(now time.Time) error {
    if s.status == StatusCancelled {
        return ErrSubscriptionAlreadyCancelled
    }

    refundCents := calculateRefundCents(s.priceCents, s.startDate, now)
    s.status = StatusCancelled
    s.cancelledAt = &now
    s.refundCents = refundCents
    return nil
}

func calculateRefundCents(priceCents int64, startDate, now time.Time) int64 {
    const billingPeriodDays = 30

    daysUsed := int64(now.Sub(startDate).Hours()) / 24

    if daysUsed <= 0 {
        return priceCents 
    }
    if daysUsed >= billingPeriodDays {
        return 0
    }
    daysRemaining := billingPeriodDays - daysUsed
    return (priceCents * daysRemaining) / billingPeriodDays
}
```

Key properties:
- All money is `int64` cents — no floating-point anywhere
- `now` comes from an injected clock, making the calculation deterministic
- Days are computed as integer truncation of hours, matching business expectation
- Edge cases are clamped: 0 days used = full refund, >= 30 days = no refund
- Integer division truncates in the customer's disfavor, which is the safe default (can be adjusted to round in customer's favor if business requires it)

---

## Q4: Test design for CancelSubscription

### What the tests prove

The cancel subscription tests use fakes for all dependencies (repository, committer, clock) and verify:

1. **Success path:** Status transitions to CANCELLED, refundCents is correct, cancelledAt matches the injected clock time.

2. **Outbox event created (durable):** The committed plan contains exactly **2 mutations** — one for the subscription state update and one for the outbox insert — applied through a single `Committer.Apply` call. This proves the outbox event is part of the atomic commit boundary, not just an in-memory artifact. The test also verifies the outbox event carries the correct event type (`SubscriptionCancelled`), aggregate ID, and refund amount.

3. **Already cancelled error:** Returns `ErrSubscriptionAlreadyCancelled` and commits nothing.

4. **Refund calculation correctness:** Tested with multiple fixed clock times — 0 days (full refund), 30+ days (zero refund), and 7 days with 1000 cents (766 cents, proving integer truncation and no float involvement).

### Test structure


### What to mock

| Dependency | Fake | Why |
|---|---|---|
| `SubscriptionRepository` | `fakeRepo` | Returns pre-built aggregates; records mutation and outbox calls |
| `Committer` | `fakeCommitter` | Captures the full Plan without touching a database |
| `Clock` | `fakeClock` | Returns deterministic time for reproducible refund math |

### What to assert

- **State:** subscription status, refundCents, cancelledAt
- **Atomicity:** plan contains both state mutation and outbox mutation
- **Outbox event:** correct type, aggregate ID, and payload data
- **Errors:** correct domain error for invalid transitions; no commit on failure
- **Math:** exact integer refund values across edge cases

---

## Q5: Business problem of ignoring the validate-customer error

```go
resp, _ := s.client.Get("https://api.billing.com/validate/" + req.CustomerID)
```

Beyond the nil pointer crash (which is a runtime bug), the critical **business** problem is **wrong decision-making under infrastructure failure**.

When the error is discarded:
1. If the HTTP call fails (network timeout, DNS failure, billing API outage), `resp` is nil and the code panics.
2. If the panic is somehow avoided (e.g., via recovery middleware), the zero-value of `result.Valid` is `false`.
3. This means: **a valid customer is rejected because the billing API was temporarily unavailable.**

The error semantics collapse: "we could not reach the billing API" becomes indistinguishable from "the customer is invalid." The system makes the **wrong business decision** — refusing to create a legitimate subscription — based on a transient infrastructure problem.

In the opposite direction, if the API returns a non-200 response that happens to contain `{"valid": true}` in an error body (or if the JSON decoder silently produces unexpected results on malformed input), an **invalid customer could be accepted**, leading to a subscription with no valid billing relationship.

The core issue is that **infrastructure failure is silently converted into a business verdict**. The correct behavior is:
- If the billing API is unreachable, return an error to the caller indicating the operation cannot be completed right now.
- Let the caller retry or queue the request.
- Never treat "I don't know" as "no."

This is especially dangerous in a subscription/billing context because wrong customer validation can lead to revenue loss (valid customers rejected) or financial liability (invalid customers accepted).
