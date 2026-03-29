package cancelsubscription_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giorgim/senior-task-2/contracts"
	"github.com/giorgim/senior-task-2/domain"
	cancelsubscription "github.com/giorgim/senior-task-2/usecases/cancel_subscription"
)

type fakeRepo struct {
	sub *domain.Subscription
	err error

	cancelCalled bool
	outboxEvents []domain.Event
}

func (f *fakeRepo) FindByID(_ context.Context, _ string) (*domain.Subscription, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sub, nil
}

func (f *fakeRepo) InsertMutation(_ *domain.Subscription) contracts.Mutation {
	return contracts.Mutation{}
}

func (f *fakeRepo) CancelMutation(sub *domain.Subscription) contracts.Mutation {
	f.cancelCalled = true
	return contracts.Mutation{
		SQL:                "UPDATE subscriptions SET status = ? WHERE id = ? AND status != ?",
		Args:               []any{sub.ID()},
		ExpectRowsAffected: true,
	}
}

func (f *fakeRepo) OutboxInsertMutation(evt domain.Event) (contracts.Mutation, error) {
	f.outboxEvents = append(f.outboxEvents, evt)
	return contracts.Mutation{
		SQL:  "INSERT INTO outbox",
		Args: []any{string(evt.Type), evt.SubscriptionID},
	}, nil
}

type fakeCommitter struct {
	applied *contracts.Plan
	err     error
}

func (f *fakeCommitter) Apply(_ context.Context, plan contracts.Plan) error {
	f.applied = &plan
	return f.err
}

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }

func activeSubscription(start time.Time) *domain.Subscription {
	sub, _ := domain.Reconstitute(
		"sub-100", "cust-100", "plan-pro", 3000,
		domain.StatusActive, start, nil, 0,
	)
	return sub
}

func cancelledSubscription(start time.Time) *domain.Subscription {
	cancelled := start.Add(10 * 24 * time.Hour)
	sub, _ := domain.Reconstitute(
		"sub-100", "cust-100", "plan-pro", 3000,
		domain.StatusCancelled, start, &cancelled, 2000,
	)
	return sub
}

func TestExecute_Success(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)

	repo := &fakeRepo{sub: activeSubscription(startDate)}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	sub, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sub.Status() != domain.StatusCancelled {
		t.Errorf("expected CANCELLED, got %s", sub.Status())
	}

	if sub.RefundCents() != 2000 {
		t.Errorf("expected refund 2000 cents, got %d", sub.RefundCents())
	}

	ca, ok := sub.CancelledAt()
	if !ok || !ca.Equal(cancelDate) {
		t.Errorf("expected cancelledAt %v, got %v (ok=%v)", cancelDate, ca, ok)
	}

	if !repo.cancelCalled {
		t.Error("expected CancelMutation to be called")
	}

	if committer.applied == nil {
		t.Fatal("expected plan to be committed")
	}
}

func TestExecute_Success_OutboxMutationCreated(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)

	repo := &fakeRepo{sub: activeSubscription(startDate)}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	_, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if committer.applied == nil {
		t.Fatal("expected plan to be committed")
	}
	if len(committer.applied.Mutations) != 2 {
		t.Fatalf("expected 2 mutations (state + outbox), got %d", len(committer.applied.Mutations))
	}

	if !strings.Contains(committer.applied.Mutations[0].SQL, "UPDATE") {
		t.Errorf("expected first mutation to be UPDATE, got %q", committer.applied.Mutations[0].SQL)
	}

	if !strings.Contains(committer.applied.Mutations[1].SQL, "outbox") {
		t.Errorf("expected second mutation to be outbox INSERT, got %q", committer.applied.Mutations[1].SQL)
	}

	if len(repo.outboxEvents) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(repo.outboxEvents))
	}
	evt := repo.outboxEvents[0]
	if evt.Type != domain.EventSubscriptionCancelled {
		t.Errorf("expected SubscriptionCancelled event, got %s", evt.Type)
	}
	if evt.SubscriptionID != "sub-100" {
		t.Errorf("expected aggregate ID sub-100, got %s", evt.SubscriptionID)
	}
	data, ok := evt.Data.(domain.SubscriptionCancelledData)
	if !ok {
		t.Fatal("event data is not SubscriptionCancelledData")
	}
	if data.RefundCents != 2000 {
		t.Errorf("expected event refund 2000, got %d", data.RefundCents)
	}
}

func TestExecute_AlreadyCancelled(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	repo := &fakeRepo{sub: cancelledSubscription(startDate)}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	_, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})
	if !errors.Is(err, domain.ErrSubscriptionAlreadyCancelled) {
		t.Fatalf("expected ErrSubscriptionAlreadyCancelled, got %v", err)
	}

	if committer.applied != nil {
		t.Error("expected no plan to be committed for already-cancelled subscription")
	}
}

func TestExecute_RefundCalculation_FullPeriodUsed(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2025, 7, 5, 0, 0, 0, 0, time.UTC)

	repo := &fakeRepo{sub: activeSubscription(startDate)}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	sub, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sub.RefundCents() != 0 {
		t.Errorf("expected 0 refund for full period used, got %d", sub.RefundCents())
	}
}

func TestExecute_RefundCalculation_SameDay(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := startDate

	repo := &fakeRepo{sub: activeSubscription(startDate)}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	sub, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sub.RefundCents() != 3000 {
		t.Errorf("expected full refund 3000 cents, got %d", sub.RefundCents())
	}
}

func TestExecute_RefundCalculation_IntegerMath(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2025, 6, 8, 0, 0, 0, 0, time.UTC)

	sub, _ := domain.Reconstitute("sub-200", "cust-200", "plan-x", 1000,
		domain.StatusActive, startDate, nil, 0)
	repo := &fakeRepo{sub: sub}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	result, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-200",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := int64(766)
	if result.RefundCents() != expected {
		t.Errorf("expected refund %d cents, got %d", expected, result.RefundCents())
	}
}

func TestExecute_SubscriptionNotFound(t *testing.T) {
	repo := &fakeRepo{err: domain.ErrSubscriptionNotFound}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: time.Now()}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	_, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "nonexistent",
	})
	if !errors.Is(err, domain.ErrSubscriptionNotFound) {
		t.Fatalf("expected ErrSubscriptionNotFound, got %v", err)
	}
}

func TestExecute_ConcurrentCancelProducesNoOutbox(t *testing.T) {
	startDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)

	repo := &fakeRepo{sub: activeSubscription(startDate)}
	committer := &fakeCommitter{err: contracts.ErrStaleWrite}
	clock := &fakeClock{now: cancelDate}

	interactor := cancelsubscription.NewInteractor(repo, committer, clock)

	_, err := interactor.Execute(context.Background(), cancelsubscription.Request{
		SubscriptionID: "sub-100",
	})

	if !errors.Is(err, domain.ErrSubscriptionAlreadyCancelled) {
		t.Fatalf("expected ErrSubscriptionAlreadyCancelled on stale cancel, got %v", err)
	}

	if committer.applied == nil {
		t.Fatal("expected plan to have been attempted")
	}
}
