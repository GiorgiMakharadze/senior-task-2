package domain

import (
	"errors"
	"testing"
	"time"
)

var fixedTime = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func TestNewSubscription_RaisesCreatedEvent(t *testing.T) {
	sub, err := NewSubscription("sub-1", "cust-1", "plan-1", 3000, fixedTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := sub.DrainEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventSubscriptionCreated {
		t.Errorf("expected SubscriptionCreated, got %s", events[0].Type)
	}
	data, ok := events[0].Data.(SubscriptionCreatedData)
	if !ok {
		t.Fatal("event data is not SubscriptionCreatedData")
	}
	if data.PriceCents != 3000 {
		t.Errorf("expected 3000 cents, got %d", data.PriceCents)
	}
}

func TestNewSubscription_InvalidPrice(t *testing.T) {
	_, err := NewSubscription("sub-1", "cust-1", "plan-1", 0, fixedTime)
	if !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("expected ErrInvalidPrice, got %v", err)
	}

	_, err = NewSubscription("sub-1", "cust-1", "plan-1", -100, fixedTime)
	if !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("expected ErrInvalidPrice for negative, got %v", err)
	}
}

func TestCancel_ActiveSubscription(t *testing.T) {
	sub, _ := NewSubscription("sub-1", "cust-1", "plan-1", 3000, fixedTime)
	sub.DrainEvents()

	cancelTime := fixedTime.Add(10 * 24 * time.Hour)
	if err := sub.Cancel(cancelTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sub.Status() != StatusCancelled {
		t.Errorf("expected CANCELLED, got %s", sub.Status())
	}
	if sub.RefundCents() != 2000 {
		t.Errorf("expected 2000, got %d", sub.RefundCents())
	}

	events := sub.DrainEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventSubscriptionCancelled {
		t.Errorf("expected SubscriptionCancelled, got %s", events[0].Type)
	}
}

func TestCancel_AlreadyCancelled(t *testing.T) {
	sub, _ := NewSubscription("sub-1", "cust-1", "plan-1", 3000, fixedTime)
	_ = sub.Cancel(fixedTime.Add(5 * 24 * time.Hour))
	sub.DrainEvents()

	err := sub.Cancel(fixedTime.Add(10 * 24 * time.Hour))
	if !errors.Is(err, ErrSubscriptionAlreadyCancelled) {
		t.Fatalf("expected ErrSubscriptionAlreadyCancelled, got %v", err)
	}

	if len(sub.DrainEvents()) != 0 {
		t.Error("expected no events on failed cancel")
	}
}

func TestReconstitute_DoesNotRaiseEvents(t *testing.T) {
	sub, err := Reconstitute("sub-1", "cust-1", "plan-1", 3000, StatusActive, fixedTime, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sub.DrainEvents()) != 0 {
		t.Error("reconstitute must not raise events")
	}
}

func TestReconstitute_InvalidState(t *testing.T) {
	_, err := Reconstitute("", "cust-1", "plan-1", 3000, StatusActive, fixedTime, nil, 0)
	if !errors.Is(err, ErrInvalidReconstitution) {
		t.Fatalf("expected ErrInvalidReconstitution for empty id, got %v", err)
	}

	_, err = Reconstitute("sub-1", "cust-1", "plan-1", 3000, Status("BOGUS"), fixedTime, nil, 0)
	if !errors.Is(err, ErrInvalidReconstitution) {
		t.Fatalf("expected ErrInvalidReconstitution for bad status, got %v", err)
	}
}

func TestRefundCalculation_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		priceCents int64
		daysUsed   int
		wantRefund int64
	}{
		{"same day", 3000, 0, 3000},
		{"one day", 3000, 1, 2900},
		{"half period", 3000, 15, 1500},
		{"full period", 3000, 30, 0},
		{"over period", 3000, 45, 0},
		{"integer truncation", 1000, 7, 766},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := fixedTime
			now := start.Add(time.Duration(tt.daysUsed) * 24 * time.Hour)
			got := calculateRefundCents(tt.priceCents, start, now)
			if got != tt.wantRefund {
				t.Errorf("calculateRefundCents(%d, %d days) = %d, want %d",
					tt.priceCents, tt.daysUsed, got, tt.wantRefund)
			}
		})
	}
}
