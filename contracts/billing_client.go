package contracts

import (
	"context"
	"time"
)

type BillingClient interface {
	ValidateCustomer(ctx context.Context, customerID string) (bool, error)
}

type Clock interface {
	Now() time.Time
}
