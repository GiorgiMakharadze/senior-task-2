package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/giorgim/senior-task-2/contracts"
	"github.com/giorgim/senior-task-2/domain"
)

var _ contracts.SubscriptionRepository = (*SubscriptionRepo)(nil)

type SubscriptionRepo struct {
	db *sql.DB
}

func NewSubscriptionRepo(db *sql.DB) *SubscriptionRepo {
	return &SubscriptionRepo{db: db}
}

func (r *SubscriptionRepo) FindByID(ctx context.Context, id string) (*domain.Subscription, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT id, customer_id, plan_id, price_cents, status, start_date, cancelled_at, refund_cents FROM subscriptions WHERE id = ?", id)

	var (
		subID       string
		customerID  string
		planID      string
		priceCents  int64
		status      string
		startDate   time.Time
		cancelledAt sql.NullTime
		refundCents int64
	)

	if err := row.Scan(&subID, &customerID, &planID, &priceCents, &status, &startDate, &cancelledAt, &refundCents); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrSubscriptionNotFound
		}
		return nil, err
	}

	var ca *time.Time
	if cancelledAt.Valid {
		ca = &cancelledAt.Time
	}

	return domain.Reconstitute(subID, customerID, planID, priceCents, domain.Status(status), startDate, ca, refundCents)
}

func (r *SubscriptionRepo) InsertMutation(sub *domain.Subscription) contracts.Mutation {
	var caArg any
	if ca, ok := sub.CancelledAt(); ok {
		caArg = ca
	}

	return contracts.Mutation{
		SQL: "INSERT INTO subscriptions (id, customer_id, plan_id, price_cents, status, start_date, cancelled_at, refund_cents) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		Args: []any{
			sub.ID(), sub.CustomerID(), sub.PlanID(), sub.PriceCents(),
			string(sub.Status()), sub.StartDate(), caArg, sub.RefundCents(),
		},
	}
}

func (r *SubscriptionRepo) CancelMutation(sub *domain.Subscription) contracts.Mutation {
	var caArg any
	if ca, ok := sub.CancelledAt(); ok {
		caArg = ca
	}

	return contracts.Mutation{
		SQL:                "UPDATE subscriptions SET status = ?, cancelled_at = ?, refund_cents = ? WHERE id = ? AND status != ?",
		Args:               []any{string(sub.Status()), caArg, sub.RefundCents(), sub.ID(), string(domain.StatusCancelled)},
		ExpectRowsAffected: true,
	}
}

func (r *SubscriptionRepo) OutboxInsertMutation(evt domain.Event) (contracts.Mutation, error) {
	payload, err := json.Marshal(evt.Data)
	if err != nil {
		return contracts.Mutation{}, fmt.Errorf("serializing outbox event payload: %w", err)
	}

	return contracts.Mutation{
		SQL:  "INSERT INTO outbox (event_type, aggregate_id, occurred_at, payload) VALUES (?, ?, ?, ?)",
		Args: []any{string(evt.Type), evt.SubscriptionID, evt.OccurredAt, payload},
	}, nil
}
