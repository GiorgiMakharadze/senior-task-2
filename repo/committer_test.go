package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/giorgim/senior-task-2/contracts"
)

type fakeResult struct {
	rowsAffected int64
}

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type fakeExecer struct {
	results []fakeResult
	calls   int
}

func (e *fakeExecer) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	if e.calls >= len(e.results) {
		return nil, errors.New("unexpected call to ExecContext")
	}
	r := e.results[e.calls]
	e.calls++
	return r, nil
}

func TestExecutePlan_StaleWriteStopsBeforeOutbox(t *testing.T) {
	exec := &fakeExecer{
		results: []fakeResult{
			{rowsAffected: 0},
			{rowsAffected: 1},
		},
	}

	plan := contracts.Plan{
		Mutations: []contracts.Mutation{
			{
				SQL:                "UPDATE subscriptions SET status = ? WHERE id = ? AND status != ?",
				Args:               []any{"cancelled", "sub-1", "cancelled"},
				ExpectRowsAffected: true,
			},
			{
				SQL:  "INSERT INTO outbox (event_type, aggregate_id) VALUES (?, ?)",
				Args: []any{"SubscriptionCancelled", "sub-1"},
			},
		},
	}

	err := executePlan(context.Background(), exec, plan)

	if !errors.Is(err, contracts.ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got %v", err)
	}

	if exec.calls != 1 {
		t.Fatalf("expected 1 exec call (cancel UPDATE only), got %d — outbox INSERT must not execute on stale cancel", exec.calls)
	}
}

func TestExecutePlan_SuccessExecutesAll(t *testing.T) {
	exec := &fakeExecer{
		results: []fakeResult{
			{rowsAffected: 1},
			{rowsAffected: 1},
		},
	}

	plan := contracts.Plan{
		Mutations: []contracts.Mutation{
			{
				SQL:                "UPDATE subscriptions SET status = ? WHERE id = ? AND status != ?",
				Args:               []any{"cancelled", "sub-1", "cancelled"},
				ExpectRowsAffected: true,
			},
			{
				SQL:  "INSERT INTO outbox (event_type, aggregate_id) VALUES (?, ?)",
				Args: []any{"SubscriptionCancelled", "sub-1"},
			},
		},
	}

	err := executePlan(context.Background(), exec, plan)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if exec.calls != 2 {
		t.Fatalf("expected 2 exec calls (cancel UPDATE + outbox INSERT), got %d", exec.calls)
	}
}
