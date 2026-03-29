package createsubscription

import (
	"context"
	"fmt"

	"github.com/giorgim/senior-task-2/contracts"
	"github.com/giorgim/senior-task-2/domain"
)

type Request struct {
	ID         string
	CustomerID string
	PlanID     string
	PriceCents int64
}

type Interactor struct {
	repo    contracts.SubscriptionRepository
	billing contracts.BillingClient
	commit  contracts.Committer
	clock   contracts.Clock
}

func NewInteractor(
	repo contracts.SubscriptionRepository,
	billing contracts.BillingClient,
	commit contracts.Committer,
	clock contracts.Clock,
) *Interactor {
	return &Interactor{
		repo:    repo,
		billing: billing,
		commit:  commit,
		clock:   clock,
	}
}

func (i *Interactor) Execute(ctx context.Context, req Request) (*domain.Subscription, error) {
	valid, err := i.billing.ValidateCustomer(ctx, req.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("validating customer %s: %w", req.CustomerID, err)
	}
	if !valid {
		return nil, domain.ErrInvalidCustomer
	}

	now := i.clock.Now()
	sub, err := domain.NewSubscription(req.ID, req.CustomerID, req.PlanID, req.PriceCents, now)
	if err != nil {
		return nil, fmt.Errorf("creating subscription: %w", err)
	}

	insertMutation := i.repo.InsertMutation(sub)

	events := sub.DrainEvents()

	mutations := []contracts.Mutation{insertMutation}
	for _, evt := range events {
		outboxMut, err := i.repo.OutboxInsertMutation(evt)
		if err != nil {
			return nil, fmt.Errorf("building outbox mutation: %w", err)
		}
		mutations = append(mutations, outboxMut)
	}

	plan := contracts.Plan{Mutations: mutations}

	if err := i.commit.Apply(ctx, plan); err != nil {
		return nil, fmt.Errorf("committing subscription: %w", err)
	}

	return sub, nil
}
