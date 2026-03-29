package domain

import "time"

type EventType string

const (
	EventSubscriptionCreated   EventType = "SubscriptionCreated"
	EventSubscriptionCancelled EventType = "SubscriptionCancelled"
)

type EventPayload interface {
	eventPayload()
}

type Event struct {
	Type           EventType
	SubscriptionID string
	OccurredAt     time.Time
	Data           EventPayload
}

type SubscriptionCreatedData struct {
	CustomerID string
	PlanID     string
	PriceCents int64
}

func (SubscriptionCreatedData) eventPayload() {}

type SubscriptionCancelledData struct {
	RefundCents int64
	CancelledAt time.Time
}

func (SubscriptionCancelledData) eventPayload() {}
