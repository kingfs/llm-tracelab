package store

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/kingfs/llm-tracelab/ent/dao"
)

// ConfigurationTransaction serializes management writes in this process. Both
// raw SQL and ent operations share one transaction. The callback must commit
// explicitly, after preparing its runtime snapshot; all other exits roll back.
//
// The transaction is also the only writer of upstream_targets/upstream_models,
// so it holds the upstream write lock for its whole duration. See
// LockUpstreamWrites and TryLockUpstreamWrites for the refresh path.
func (s *Store) ConfigurationTransaction(ctx context.Context, apply func(*Store, func() error) error) error {
	if s == nil || s.db == nil || s.shared == nil {
		return fmt.Errorf("configuration transaction: %w", errorsNewStoreClosed())
	}
	if apply == nil {
		return fmt.Errorf("configuration transaction: apply callback is required")
	}
	if s.TransactionScoped() {
		return fmt.Errorf("configuration transaction: %w", ErrNestedTransaction)
	}
	s.shared.configMu.Lock()
	defer s.shared.configMu.Unlock()
	s.shared.upstreamMu.Lock()
	defer s.shared.upstreamMu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	name := dialect.SQLite
	if s.driver == "postgres" {
		name = dialect.Postgres
	}
	driver := &configurationTxDriver{Driver: entsql.NewDriver(name, entsql.Conn{ExecQuerier: tx})}
	view := &Store{
		db:        &rebindingDB{DB: s.db.DB, tx: tx, driver: s.driver},
		client:    dao.NewClient(dao.Driver(driver)),
		outputDir: s.outputDir, dbPath: s.dbPath, driver: s.driver,
		secrets: s.secrets, useSessionSummaryRead: s.useSessionSummaryRead,
		shared: s.shared,
	}
	committed := false
	commit := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if err := apply(view, commit); err != nil {
		return err
	}
	if !committed {
		return fmt.Errorf("configuration transaction was not committed")
	}
	return nil
}

// TransactionScoped reports whether this store view writes through one open
// transaction instead of the connection pool. A transaction-scoped store
// already holds the configuration and upstream write locks.
func (s *Store) TransactionScoped() bool {
	return s != nil && s.db != nil && s.db.tx != nil
}

// LockUpstreamWrites blocks until this process may write upstream_targets and
// upstream_models on its own, and returns the release function.
//
// ConfigurationTransaction holds the same lock for its whole duration, so a
// store that already runs inside one must not call this again; that case
// reports ErrNestedTransaction instead of deadlocking.
func (s *Store) LockUpstreamWrites() (func(), error) {
	if s == nil || s.shared == nil {
		return nil, fmt.Errorf("lock upstream writes: %w", errorsNewStoreClosed())
	}
	if s.TransactionScoped() {
		return nil, fmt.Errorf("lock upstream writes: %w", ErrNestedTransaction)
	}
	s.shared.upstreamMu.Lock()
	return s.shared.upstreamMu.Unlock, nil
}

// TryLockUpstreamWrites is the non-blocking form of LockUpstreamWrites. It
// reports locked=false when a configuration change currently owns the lock,
// which lets best-effort refresh persistence skip the write and stay
// up-to-date in memory instead of stalling its caller (for example a proxy
// retry).
func (s *Store) TryLockUpstreamWrites() (release func(), locked bool, err error) {
	if s == nil || s.shared == nil {
		return nil, false, fmt.Errorf("lock upstream writes: %w", errorsNewStoreClosed())
	}
	if s.TransactionScoped() {
		return nil, false, fmt.Errorf("lock upstream writes: %w", ErrNestedTransaction)
	}
	if !s.shared.upstreamMu.TryLock() {
		return nil, false, nil
	}
	return s.shared.upstreamMu.Unlock, true, nil
}

// Store helpers may open nested ent transactions. Their commits are scoped to
// the outer configuration operation, so a later failure rolls back everything.
type configurationTxDriver struct{ *entsql.Driver }

func (d *configurationTxDriver) Tx(context.Context) (dialect.Tx, error) {
	return dialect.NopTx(d), nil
}
