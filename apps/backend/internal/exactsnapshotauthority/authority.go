// Package exactsnapshotauthority seals one SQLite writer identity for exact snapshots.
package exactsnapshotauthority

import (
	"context"
	"errors"

	"github.com/jmoiron/sqlx"
)

var ErrUnavailable = errors.New("exact snapshot authority unavailable")

// Authority binds a composite exact read to one bootstrap-issued SQLite writer.
// It has no exported fields, so readers can require its transaction capability
// instead of accepting an arbitrary sqlx transaction.
type Authority struct{ writer *sqlx.DB }

// Transaction is issued only by Authority.Begin. Its database transaction is
// intentionally private to authority-bound readers.
type Transaction struct {
	authority *Authority
	tx        *sqlx.Tx
}

// NewSQLite issues an authority for one SQLite bootstrap writer.
func NewSQLite(writer *sqlx.DB) (*Authority, error) {
	if writer == nil || writer.DriverName() == "pgx" {
		return nil, ErrUnavailable
	}
	return &Authority{writer: writer}, nil
}

func (a *Authority) Matches(writer *sqlx.DB) bool { return a != nil && a.writer == writer }

func (a *Authority) Begin(ctx context.Context) (*Transaction, error) {
	if a == nil {
		return nil, ErrUnavailable
	}
	tx, err := a.writer.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Transaction{authority: a, tx: tx}, nil
}

// Matches reports whether this transaction was issued by authority.
func (t *Transaction) Matches(authority *Authority) bool {
	return t != nil && t.authority == authority && t.tx != nil
}

// SQLX exposes the transaction only to bound reader implementations.
func (t *Transaction) SQLX() *sqlx.Tx {
	if t == nil {
		return nil
	}
	return t.tx
}

// Commit resolves the authority transaction after every bound reader has
// materialized its evidence.
func (t *Transaction) Commit() error {
	if t == nil || t.tx == nil {
		return ErrUnavailable
	}
	return t.tx.Commit()
}

// Rollback abandons every materialized snapshot from the authority transaction.
func (t *Transaction) Rollback() error {
	if t == nil || t.tx == nil {
		return ErrUnavailable
	}
	return t.tx.Rollback()
}
