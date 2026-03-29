package createsubscription_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giorgim/senior-task-2/contracts"
	"github.com/giorgim/senior-task-2/domain"
	createsubscription "github.com/giorgim/senior-task-2/usecases/create_subscription"
)

type fakeBilling struct {
	valid bool
	err   error
}

func (f *fakeBilling) ValidateCustomer(_ context.Context, _ string) (bool, error) {
	return f.valid, f.err
}

type fakeRepo struct {
	insertCalled bool
	lastInsert   *domain.Subscription
	outboxEvents []domain.Event
}

func (f *fakeRepo) FindByID(_ context.Context, _ string) (*domain.Subscription, error) {
	return nil, domain.ErrSubscriptionNotFound
}

func (f *fakeRepo) InsertMutation(sub *domain.Subscription) contracts.Mutation {
	f.insertCalled = true
	f.lastInsert = sub
	return contracts.Mutation{SQL: "INSERT INTO subscriptions", Args: []any{sub.ID()}}
}

func (f *fakeRepo) CancelMutation(_ *domain.Subscription) contracts.Mutation {
	return contracts.Mutation{}
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

func TestExecute_Success(t *testing.T) {
	fixedTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	billing := &fakeBilling{valid: true}
	repo := &fakeRepo{}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: fixedTime}

	interactor := createsubscription.NewInteractor(repo, billing, committer, clock)

	req := createsubscription.Request{
		ID:         "sub-001",
		CustomerID: "cust-001",
		PlanID:     "plan-basic",
		PriceCents: 2999,
	}

	sub, err := interactor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sub.ID() != "sub-001" {
		t.Errorf("expected ID sub-001, got %s", sub.ID())
	}
	if sub.CustomerID() != "cust-001" {
		t.Errorf("expected CustomerID cust-001, got %s", sub.CustomerID())
	}
	if sub.PriceCents() != 2999 {
		t.Errorf("expected PriceCents 2999, got %d", sub.PriceCents())
	}
	if sub.Status() != domain.StatusActive {
		t.Errorf("expected status ACTIVE, got %s", sub.Status())
	}

	if !repo.insertCalled {
		t.Error("expected InsertMutation to be called")
	}

	if committer.applied == nil {
		t.Fatal("expected plan to be committed")
	}
	if len(committer.applied.Mutations) != 2 {
		t.Fatalf("expected 2 mutations (insert + outbox), got %d", len(committer.applied.Mutations))
	}
	if !strings.Contains(committer.applied.Mutations[0].SQL, "INSERT INTO subscriptions") {
		t.Errorf("expected first mutation to be subscription INSERT, got %q", committer.applied.Mutations[0].SQL)
	}
	if !strings.Contains(committer.applied.Mutations[1].SQL, "outbox") {
		t.Errorf("expected second mutation to be outbox INSERT, got %q", committer.applied.Mutations[1].SQL)
	}

	if len(repo.outboxEvents) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(repo.outboxEvents))
	}
	if repo.outboxEvents[0].Type != domain.EventSubscriptionCreated {
		t.Errorf("expected SubscriptionCreated event, got %s", repo.outboxEvents[0].Type)
	}
	data, ok := repo.outboxEvents[0].Data.(domain.SubscriptionCreatedData)
	if !ok {
		t.Fatal("event data is not SubscriptionCreatedData")
	}
	if data.PriceCents != 2999 {
		t.Errorf("expected event PriceCents 2999, got %d", data.PriceCents)
	}
}

func TestExecute_InvalidCustomer(t *testing.T) {
	billing := &fakeBilling{valid: false}
	repo := &fakeRepo{}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: time.Now()}

	interactor := createsubscription.NewInteractor(repo, billing, committer, clock)

	req := createsubscription.Request{
		ID:         "sub-002",
		CustomerID: "bad-cust",
		PlanID:     "plan-basic",
		PriceCents: 1000,
	}

	_, err := interactor.Execute(context.Background(), req)
	if !errors.Is(err, domain.ErrInvalidCustomer) {
		t.Fatalf("expected ErrInvalidCustomer, got %v", err)
	}

	if committer.applied != nil {
		t.Error("expected no plan to be committed for invalid customer")
	}
}

func TestExecute_BillingAPIError(t *testing.T) {
	billing := &fakeBilling{err: errors.New("connection refused")}
	repo := &fakeRepo{}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: time.Now()}

	interactor := createsubscription.NewInteractor(repo, billing, committer, clock)

	req := createsubscription.Request{
		ID:         "sub-003",
		CustomerID: "cust-003",
		PlanID:     "plan-basic",
		PriceCents: 1000,
	}

	_, err := interactor.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when billing API fails")
	}

	if committer.applied != nil {
		t.Error("expected no plan to be committed when billing API fails")
	}
}

func TestExecute_InvalidPrice(t *testing.T) {
	billing := &fakeBilling{valid: true}
	repo := &fakeRepo{}
	committer := &fakeCommitter{}
	clock := &fakeClock{now: time.Now()}

	interactor := createsubscription.NewInteractor(repo, billing, committer, clock)

	req := createsubscription.Request{
		ID:         "sub-004",
		CustomerID: "cust-004",
		PlanID:     "plan-basic",
		PriceCents: 0,
	}

	_, err := interactor.Execute(context.Background(), req)
	if !errors.Is(err, domain.ErrInvalidPrice) {
		t.Fatalf("expected ErrInvalidPrice, got %v", err)
	}
}
