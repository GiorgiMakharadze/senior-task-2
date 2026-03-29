# Code Review: Broken Subscription Implementation

## Issue 1: Service directly depends on *sql.DB
- Category: Layer Violation
- Severity: CRITICAL
- Problem: `SubscriptionService` holds a concrete `*sql.DB`, coupling business logic directly to SQL infrastructure. There is no repository interface or abstraction boundary.
- Why it matters: The usecase layer cannot be tested without a real database. Swapping storage engines or adding caching requires rewriting business logic. This violates dependency inversion — the core should depend on abstractions, not infrastructure.

## Issue 2: Service directly depends on *http.Client
- Category: Layer Violation
- Severity: CRITICAL
- Problem: `SubscriptionService` holds a concrete `*http.Client` and makes raw HTTP calls inline to validate customers and issue refunds.
- Why it matters: Business logic is coupled to HTTP transport details (URL construction, JSON decoding, response handling). This cannot be tested without a live HTTP server or test server. The external billing API should be behind an interface.

## Issue 3: HTTP call (customer validation) embedded in business logic
- Category: Layer Violation
- Severity: CRITICAL
- Problem: `CreateSubscription` directly calls `s.client.Get(...)` to validate the customer. The URL is hardcoded, and the HTTP call is interleaved with domain logic.
- Why it matters: Domain and usecase logic should not know about HTTP. This mixes transport, serialization, and business orchestration in one method. It makes the code untestable and tightly coupled to a specific external API shape.

## Issue 4: HTTP call (refund) embedded in business logic
- Category: Layer Violation
- Severity: CRITICAL
- Problem: `CancelSubscription` directly calls `s.client.Post(...)` to issue a refund, constructing JSON and calling an external API inline.
- Why it matters: The refund side effect is buried inside cancellation logic with no error handling, no retry strategy, and no separation from the state transition. If the refund call fails silently, the subscription is still marked cancelled with no record of the failed refund.

## Issue 5: No domain aggregate — Subscription is an anemic data bag
- Category: Domain Purity
- Severity: CRITICAL
- Problem: `Subscription` is a plain struct with all public fields and no methods. There are no invariant checks, no controlled state transitions, and no encapsulation.
- Why it matters: Any code can set any field to any value, bypassing business rules. There is no guarantee that a subscription transitions through valid states. The "domain" is just a DTO with no behavioral ownership.

## Issue 6: No domain events
- Category: Domain Purity
- Severity: WARNING
- Problem: Creation and cancellation produce no domain events. There is no change tracking or event sourcing capability.
- Why it matters: Downstream processes (notifications, analytics, refund processing, audit) have no way to react to lifecycle changes. The system cannot support event-driven architecture or outbox patterns.

## Issue 7: Status is a raw string with no type safety
- Category: Domain Purity
- Severity: WARNING
- Problem: `Status` is `string`, and status values like `"ACTIVE"` and `"CANCELLED"` are bare string literals scattered through the code.
- Why it matters: Any typo creates a silent bug. There is no compiler enforcement of valid statuses. Adding new statuses requires searching for string literals across the codebase.

## Issue 8: UUID generation and time.Now() inside business logic
- Category: Domain Purity
- Severity: WARNING
- Problem: `CreateSubscription` calls `uuid.New().String()` and `time.Now()` directly. These are hidden infrastructure dependencies embedded in the business layer.
- Why it matters: The ID and timestamp are non-deterministic, making tests unreproducible. The business layer owns infrastructure concerns it should not. ID generation strategy and clock behavior should be injected.

## Issue 9: Ignored error on HTTP GET for customer validation
- Category: Error Handling
- Severity: CRITICAL
- Problem: `resp, _ := s.client.Get(...)` discards the error. If the HTTP call fails, `resp` is nil, and `resp.Body.Close()` on the next line causes a nil pointer panic.
- Why it matters: Beyond the crash, ignoring this error means a network failure, DNS issue, or timeout is silently treated as "customer invalid" (since `result.Valid` defaults to `false`). This causes valid customers to be rejected due to infrastructure problems — a wrong business decision from collapsed error semantics.

## Issue 10: Ignored errors on row.Scan, json.Decode, json.Marshal, and HTTP POST
- Category: Error Handling
- Severity: CRITICAL
- Problem: Multiple error returns are discarded: `row.Scan(...)`, `json.NewDecoder(...).Decode(...)`, `json.Marshal(...)`, and the refund `s.client.Post(...)`. None are checked.
- Why it matters: A scan failure means the code operates on zero-valued fields (empty ID, zero price, epoch start date), producing incorrect refund calculations and phantom data. A failed refund POST means money is not returned to the customer with no error signal.

## Issue 11: float64 used for money
- Category: Money Handling
- Severity: CRITICAL
- Problem: `Price` is `float64`, and the refund calculation uses floating-point arithmetic: `sub.Price * (30 - daysUsed) / 30`.
- Why it matters: Floating-point cannot represent decimal currency values exactly. Operations like `29.99 * 20 / 30` produce rounding errors that accumulate over many transactions. Financial calculations must use integer cents (int64) or a decimal type to avoid silent money loss or gain.

## Issue 12: time.Since() used for business date calculation
- Category: Testability
- Severity: CRITICAL
- Problem: `CancelSubscription` uses `time.Since(sub.StartDate)` which calls the wall clock internally. The result changes every time the code runs.
- Why it matters: The refund calculation is non-deterministic and untestable. You cannot write a reliable test that asserts a specific refund amount because the elapsed time changes between runs. Business time calculations must use an injected clock.

## Issue 13: No interfaces — impossible to test without real infrastructure
- Category: Testability
- Severity: CRITICAL
- Problem: `SubscriptionService` takes concrete `*sql.DB` and `*http.Client`. There are no interfaces for the repository, billing client, or clock.
- Why it matters: Unit tests require a running database and HTTP server. This makes tests slow, flaky, and environment-dependent. Proper testability requires dependency inversion through interfaces that can be replaced with fakes in tests.

## Issue 14: SELECT * is fragile
- Category: Layer Violation
- Severity: WARNING
- Problem: `CancelSubscription` uses `SELECT * FROM subscriptions` and relies on positional `Scan` to match column order.
- Why it matters: Adding, removing, or reordering columns in the table silently breaks the scan. The query should explicitly name columns to ensure the code matches the schema.

## Issue 15: Refund can be negative
- Category: Money Handling
- Severity: WARNING
- Problem: The refund formula `sub.Price * (30 - daysUsed) / 30` produces a negative value if `daysUsed > 30`. There is no clamping or bounds check.
- Why it matters: A negative refund could be interpreted as charging the customer additional money. The calculation must clamp to zero when the billing period has fully elapsed.

## Issue 16: CustomerID used directly in URL without escaping
- Category: Error Handling
- Severity: WARNING
- Problem: `"https://api.billing.com/validate/" + req.CustomerID` concatenates user input directly into a URL path with no escaping or validation.
- Why it matters: A customer ID containing slashes, query parameters, or special characters can corrupt the URL, potentially hitting wrong endpoints or enabling injection. Input used in URLs must be properly escaped.
