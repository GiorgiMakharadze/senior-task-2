package domain

import (
	"fmt"
	"time"
)

type Status string

const (
	StatusActive    Status = "ACTIVE"
	StatusCancelled Status = "CANCELLED"
)

func validStatus(s Status) bool {
	return s == StatusActive || s == StatusCancelled
}

type Subscription struct {
	id          string
	customerID  string
	planID      string
	priceCents  int64
	status      Status
	startDate   time.Time
	cancelledAt *time.Time
	refundCents int64
	events      []Event
}

func NewSubscription(id, customerID, planID string, priceCents int64, now time.Time) (*Subscription, error) {
	if priceCents <= 0 {
		return nil, ErrInvalidPrice
	}

	s := &Subscription{
		id:         id,
		customerID: customerID,
		planID:     planID,
		priceCents: priceCents,
		status:     StatusActive,
		startDate:  now,
	}

	s.events = append(s.events, Event{
		Type:           EventSubscriptionCreated,
		SubscriptionID: id,
		OccurredAt:     now,
		Data: SubscriptionCreatedData{
			CustomerID: customerID,
			PlanID:     planID,
			PriceCents: priceCents,
		},
	})

	return s, nil
}

func Reconstitute(id, customerID, planID string, priceCents int64, status Status, startDate time.Time, cancelledAt *time.Time, refundCents int64) (*Subscription, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty id", ErrInvalidReconstitution)
	}
	if !validStatus(status) {
		return nil, fmt.Errorf("%w: unknown status %q", ErrInvalidReconstitution, status)
	}

	return &Subscription{
		id:          id,
		customerID:  customerID,
		planID:      planID,
		priceCents:  priceCents,
		status:      status,
		startDate:   startDate,
		cancelledAt: cancelledAt,
		refundCents: refundCents,
	}, nil
}

func (s *Subscription) Cancel(now time.Time) error {
	if s.status == StatusCancelled {
		return ErrSubscriptionAlreadyCancelled
	}

	refundCents := calculateRefundCents(s.priceCents, s.startDate, now)

	s.status = StatusCancelled
	t := now
	s.cancelledAt = &t
	s.refundCents = refundCents

	s.events = append(s.events, Event{
		Type:           EventSubscriptionCancelled,
		SubscriptionID: s.id,
		OccurredAt:     now,
		Data: SubscriptionCancelledData{
			RefundCents: refundCents,
			CancelledAt: now,
		},
	})

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

func (s *Subscription) ID() string           { return s.id }
func (s *Subscription) CustomerID() string   { return s.customerID }
func (s *Subscription) PlanID() string       { return s.planID }
func (s *Subscription) PriceCents() int64    { return s.priceCents }
func (s *Subscription) Status() Status       { return s.status }
func (s *Subscription) StartDate() time.Time { return s.startDate }
func (s *Subscription) RefundCents() int64   { return s.refundCents }

func (s *Subscription) CancelledAt() (time.Time, bool) {
	if s.cancelledAt == nil {
		return time.Time{}, false
	}
	return *s.cancelledAt, true
}

func (s *Subscription) DrainEvents() []Event {
	out := make([]Event, len(s.events))
	copy(out, s.events)
	s.events = nil
	return out
}
