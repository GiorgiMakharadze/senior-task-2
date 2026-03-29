package contracts

import (
	"context"
	"errors"

	"github.com/giorgim/senior-task-2/domain"
)

var ErrStaleWrite = errors.New("stale write: no rows affected")

type Mutation struct {
	SQL  string
	Args []any

	ExpectRowsAffected bool
}

type Plan struct {
	Mutations []Mutation
}

type SubscriptionRepository interface {
	FindByID(ctx context.Context, id string) (*domain.Subscription, error)

	InsertMutation(sub *domain.Subscription) Mutation

	CancelMutation(sub *domain.Subscription) Mutation

	OutboxInsertMutation(evt domain.Event) (Mutation, error)
}

type Committer interface {
	Apply(ctx context.Context, plan Plan) error
}
