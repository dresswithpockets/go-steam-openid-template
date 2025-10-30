package db

import (
	"context"
	"errors"

	"github.com/Masterminds/squirrel"
	"github.com/dresswithpockets/go-steam-openid-example/db/query"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rotisserie/eris"
)

type Actions struct {
	pool    *pgxpool.Pool
	queries *query.Queries
}

func NewActions(pool *pgxpool.Pool, queries *query.Queries) *Actions {
	return &Actions{
		queries: queries,
		pool:    pool,
	}
}

func (a *Actions) Builder() squirrel.StatementBuilderType {
	return squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
}

func (a *Actions) Exec(ctx context.Context, sqlizer squirrel.Sqlizer) (pgconn.CommandTag, error) {
	sql, args, sqlErr := sqlizer.ToSql()
	if sqlErr != nil {
		return pgconn.CommandTag{}, sqlErr
	}

	return a.pool.Exec(ctx, sql, args...)
}

func (a *Actions) Query(ctx context.Context, sqlizer squirrel.Sqlizer) (pgx.Rows, error) {
	sql, args, sqlErr := sqlizer.ToSql()
	if sqlErr != nil {
		return nil, sqlErr
	}

	return a.pool.Query(ctx, sql, args...)
}

func (a *Actions) QueryRow(ctx context.Context, sqlizer squirrel.Sqlizer) (pgx.Row, error) {
	sql, args, sqlErr := sqlizer.ToSql()
	if sqlErr != nil {
		return nil, sqlErr
	}

	return a.pool.QueryRow(ctx, sql, args...), nil
}

func (a *Actions) ScanRow(ctx context.Context, sqlizer squirrel.Sqlizer, dest ...any) error {
	sql, args, sqlErr := sqlizer.ToSql()
	if sqlErr != nil {
		return sqlErr
	}

	return a.pool.QueryRow(ctx, sql, args...).Scan(dest...)
}

func (a *Actions) Transact(ctx context.Context, do func(context.Context, *query.Queries) error) (err error) {
	var tx pgx.Tx
	tx, err = a.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return eris.Wrap(err, "error beginning transaction")
	}

	qtx := a.queries.WithTx(tx)
	if err = do(ctx, qtx); err != nil {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil {
			return eris.Wrap(errors.Join(err, rollbackErr), "error rolling back transaction")
		}

		return eris.Wrap(err, "error in transaction body, so it was rolled back")
	}

	if err = tx.Commit(ctx); err != nil {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil {
			return eris.Wrap(errors.Join(err, rollbackErr), "error rolling back transaction after commit error")
		}

		return eris.Wrap(err, "error in committing transaction, so it was rolled back")
	}

	return nil
}
