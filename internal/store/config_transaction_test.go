package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func configTransactionTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestConfigurationTransactionRejectsNestedTransactions(t *testing.T) {
	st := configTransactionTestStore(t)
	var nestedConfiguration, nestedBegin, nestedBeginTx error
	err := st.ConfigurationTransaction(context.Background(), func(tx *Store, commit func() error) error {
		if !tx.TransactionScoped() {
			t.Error("configuration view is not transaction scoped")
		}
		if st.TransactionScoped() {
			t.Error("base store became transaction scoped")
		}
		nestedConfiguration = tx.ConfigurationTransaction(context.Background(), func(*Store, func() error) error {
			return nil
		})
		if _, _, err := tx.TryLockUpstreamWrites(); !errors.Is(err, ErrNestedTransaction) {
			t.Errorf("TryLockUpstreamWrites() error = %v, want ErrNestedTransaction", err)
		}
		if _, err := tx.LockUpstreamWrites(); !errors.Is(err, ErrNestedTransaction) {
			t.Errorf("LockUpstreamWrites() error = %v, want ErrNestedTransaction", err)
		}
		_, nestedBeginTx = tx.db.BeginTx(context.Background(), nil)
		_, nestedBegin = tx.db.Begin()
		return commit()
	})
	if err != nil {
		t.Fatalf("ConfigurationTransaction() error = %v", err)
	}
	for name, err := range map[string]error{"ConfigurationTransaction": nestedConfiguration, "BeginTx": nestedBeginTx, "Begin": nestedBegin} {
		if !errors.Is(err, ErrNestedTransaction) {
			t.Errorf("nested %s error = %v, want ErrNestedTransaction", name, err)
		}
	}
}

func TestConfigurationTransactionLeavesBaseStoreTransactionsUsable(t *testing.T) {
	st := configTransactionTestStore(t)
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if st.TransactionScoped() {
		t.Fatal("base store reported itself as transaction scoped")
	}
}

func TestConfigurationTransactionRejectsUnusableInputs(t *testing.T) {
	if err := (*Store)(nil).ConfigurationTransaction(context.Background(), func(*Store, func() error) error { return nil }); err == nil {
		t.Fatal("nil store accepted a configuration transaction")
	}
	st := configTransactionTestStore(t)
	if err := st.ConfigurationTransaction(context.Background(), nil); err == nil {
		t.Fatal("nil apply callback was accepted")
	}
	release, err := st.LockUpstreamWrites()
	if err != nil {
		t.Fatalf("LockUpstreamWrites() on an idle store error = %v", err)
	}
	release()
}

func TestConfigurationTransactionViewSharesEventSubscribers(t *testing.T) {
	st := configTransactionTestStore(t)
	notifications, cancel := st.SubscribeSystemEvents(1)
	defer cancel()

	err := st.ConfigurationTransaction(context.Background(), func(tx *Store, commit func() error) error {
		if _, err := tx.UpsertSystemEvent(SystemEvent{Fingerprint: "fp-config", Source: "monitor", Category: "configuration", Severity: "info", Title: "config"}); err != nil {
			return err
		}
		return commit()
	})
	if err != nil {
		t.Fatalf("ConfigurationTransaction() error = %v", err)
	}

	select {
	case notification := <-notifications:
		if notification.Sequence != 1 {
			t.Fatalf("notification sequence = %d, want 1", notification.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("configuration view did not publish system events to the base store subscribers")
	}
}

func TestLockUpstreamWritesIsExclusiveAndNonBlockingVariantReportsBusy(t *testing.T) {
	st := configTransactionTestStore(t)
	release, err := st.LockUpstreamWrites()
	if err != nil {
		t.Fatalf("LockUpstreamWrites() error = %v", err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	if _, locked, err := st.TryLockUpstreamWrites(); err != nil {
		t.Fatalf("TryLockUpstreamWrites() error = %v", err)
	} else if locked {
		t.Fatal("TryLockUpstreamWrites() reported a free lock while it was held")
	}

	acquired := make(chan struct{})
	go func() {
		inner, err := st.LockUpstreamWrites()
		if err != nil {
			t.Errorf("LockUpstreamWrites() error = %v", err)
			close(acquired)
			return
		}
		inner()
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("LockUpstreamWrites() did not wait for the current holder")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	released = true
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("LockUpstreamWrites() did not unblock after release")
	}
}

func TestDeleteAppSettingRemovesOnlyTheRequestedKey(t *testing.T) {
	st := configTransactionTestStore(t)
	ctx := context.Background()
	if err := st.SaveAppSettingJSON(ctx, "keep", map[string]string{"value": "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAppSettingJSON(ctx, "drop", map[string]string{"value": "drop"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.DeleteAppSetting(ctx, "drop")
	if err != nil {
		t.Fatalf("DeleteAppSetting() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteAppSetting() reported no deletion for an existing key")
	}
	var out map[string]string
	found, err := st.LoadAppSettingJSON(ctx, "drop", &out)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatalf("deleted setting is still readable: %v", out)
	}
	if found, err := st.LoadAppSettingJSON(ctx, "keep", &out); err != nil || !found {
		t.Fatalf("unrelated setting changed: found=%v err=%v", found, err)
	}
	deleted, err = st.DeleteAppSetting(ctx, "drop")
	if err != nil {
		t.Fatalf("DeleteAppSetting() error = %v", err)
	}
	if deleted {
		t.Fatal("DeleteAppSetting() reported a deletion for a missing key")
	}
}
