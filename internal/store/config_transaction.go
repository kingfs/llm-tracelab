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
func (s *Store) ConfigurationTransaction(ctx context.Context, apply func(*Store, func() error) error) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
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

// Store helpers may open nested ent transactions. Their commits are scoped to
// the outer configuration operation, so a later failure rolls back everything.
type configurationTxDriver struct{ *entsql.Driver }

func (d *configurationTxDriver) Tx(context.Context) (dialect.Tx, error) {
	return dialect.NopTx(d), nil
}
