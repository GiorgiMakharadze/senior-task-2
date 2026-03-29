package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/giorgim/senior-task-2/contracts"
)

var _ contracts.Committer = (*TxCommitter)(nil)

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type TxCommitter struct {
	db *sql.DB
}

func NewTxCommitter(db *sql.DB) *TxCommitter {
	return &TxCommitter{db: db}
}

func (c *TxCommitter) Apply(ctx context.Context, plan contracts.Plan) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := executePlan(ctx, tx, plan); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

func executePlan(ctx context.Context, exec execer, plan contracts.Plan) error {
	for i, mut := range plan.Mutations {
		res, err := exec.ExecContext(ctx, mut.SQL, mut.Args...)
		if err != nil {
			return fmt.Errorf("executing mutation %d: %w", i, err)
		}

		if mut.ExpectRowsAffected {
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("checking rows affected for mutation %d: %w", i, err)
			}
			if n == 0 {
				return contracts.ErrStaleWrite
			}
		}
	}
	return nil
}
