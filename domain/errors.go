package domain

import "errors"

var (
	ErrSubscriptionAlreadyCancelled = errors.New("subscription is already cancelled")
	ErrInvalidCustomer              = errors.New("invalid customer")
	ErrSubscriptionNotFound         = errors.New("subscription not found")
	ErrInvalidPrice                 = errors.New("price must be positive")
	ErrNegativeRefund               = errors.New("refund amount must not be negative")
	ErrInvalidReconstitution        = errors.New("cannot reconstitute subscription: invalid state")
)
