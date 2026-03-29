package cancelsubscription

import (
	"context"
	"errors"
	"fmt"

	"github.com/giorgim/senior-task-2/contracts"
	"github.com/giorgim/senior-task-2/domain"
)

type Request struct {
	SubscriptionID string
}

type Interactor struct {
	repo   contracts.SubscriptionRepository
	commit contracts.Committer
	clock  contracts.Clock
}

func NewInteractor(
	repo contracts.SubscriptionRepository,
	commit contracts.Committer,
	clock contracts.Clock,
) *Interactor {
	return &Interactor{
		repo:   repo,
		commit: commit,
		clock:  clock,
	}
}

func (i *Interactor) Execute(ctx context.Context, req Request) (*domain.Subscription, error) {
	sub, err := i.repo.FindByID(ctx, req.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("loading subscription %s: %w", req.SubscriptionID, err)
	}

	now := i.clock.Now()
	if err := sub.Cancel(now); err != nil {
		return nil, err
	}

	cancelMut := i.repo.CancelMutation(sub)

	events := sub.DrainEvents()

	mutations := []contracts.Mutation{cancelMut}
	for _, evt := range events {
		outboxMut, err := i.repo.OutboxInsertMutation(evt)
		if err != nil {
			return nil, fmt.Errorf("building outbox mutation: %w", err)
		}
		mutations = append(mutations, outboxMut)
	}

	plan := contracts.Plan{Mutations: mutations}

	if err := i.commit.Apply(ctx, plan); err != nil {
		if errors.Is(err, contracts.ErrStaleWrite) {
			return nil, domain.ErrSubscriptionAlreadyCancelled
		}
		return nil, fmt.Errorf("committing cancellation for %s: %w", req.SubscriptionID, err)
	}

	return sub, nil
}
