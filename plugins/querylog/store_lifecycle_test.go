package querylog

import (
	"path/filepath"
	"testing"
	"time"
)

func resetStoreSingleton(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = CloseStore() })
}

func TestReconcileStore_EmptyPath_StaysNil(t *testing.T) {
	resetStoreSingleton(t)

	if err := ReconcileStore(StoreConfig{}); err != nil {
		t.Fatalf("ReconcileStore: %v", err)
	}
	if CurrentStore() != nil {
		t.Fatal("expected CurrentStore() to stay nil when Path is empty")
	}
}

func TestReconcileStore_OpensThenClosesOnDisable(t *testing.T) {
	resetStoreSingleton(t)
	dbPath := filepath.Join(t.TempDir(), "querylog.db")

	if err := ReconcileStore(StoreConfig{Path: dbPath}); err != nil {
		t.Fatalf("ReconcileStore (enable): %v", err)
	}
	first := CurrentStore()
	if first == nil {
		t.Fatal("expected CurrentStore() to be set after enabling")
	}

	// Simulates the user turning Query Log off: this is the exact
	// transition setup() could never reach on its own (dev-docs/query-log.md)
	// since the "querylog" Corefile stanza — and therefore setup() — simply
	// isn't present once it's disabled.
	if err := ReconcileStore(StoreConfig{}); err != nil {
		t.Fatalf("ReconcileStore (disable): %v", err)
	}
	if CurrentStore() != nil {
		t.Fatal("expected CurrentStore() to be nil after disabling")
	}
}

func TestReconcileStore_SamePath_UpdatesTuningInPlace(t *testing.T) {
	resetStoreSingleton(t)
	dbPath := filepath.Join(t.TempDir(), "querylog.db")

	if err := ReconcileStore(StoreConfig{Path: dbPath, RetentionDays: 3}); err != nil {
		t.Fatalf("ReconcileStore (open): %v", err)
	}
	first := CurrentStore()

	if err := ReconcileStore(StoreConfig{Path: dbPath, RetentionDays: 9}); err != nil {
		t.Fatalf("ReconcileStore (retune): %v", err)
	}
	second := CurrentStore()

	if first != second {
		t.Fatal("expected an unchanged-path reconcile to reuse the existing Store, not reopen it")
	}
	if got := second.retentionDays.Load(); got != 9 {
		t.Fatalf("retentionDays = %d, want 9 (SetTuning should apply in place)", got)
	}
}

func TestReconcileStore_PathChange_ReopensStore(t *testing.T) {
	resetStoreSingleton(t)
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.db")
	pathB := filepath.Join(dir, "b.db")

	if err := ReconcileStore(StoreConfig{Path: pathA}); err != nil {
		t.Fatalf("ReconcileStore (a): %v", err)
	}
	first := CurrentStore()

	if err := ReconcileStore(StoreConfig{Path: pathB}); err != nil {
		t.Fatalf("ReconcileStore (b): %v", err)
	}
	second := CurrentStore()

	if first == second {
		t.Fatal("expected a path change to open a fresh Store")
	}
	if second.Path() != pathB {
		t.Fatalf("CurrentStore().Path() = %q, want %q", second.Path(), pathB)
	}
}

func TestCloseStore_NoStoreConfigured_NoOp(t *testing.T) {
	resetStoreSingleton(t)
	if err := CloseStore(); err != nil {
		t.Fatalf("CloseStore with nothing open: %v", err)
	}
}

func TestCloseStore_FlushesAndClearsSingleton(t *testing.T) {
	resetStoreSingleton(t)
	dbPath := filepath.Join(t.TempDir(), "querylog.db")

	if err := ReconcileStore(StoreConfig{Path: dbPath, FlushInterval: time.Hour}); err != nil {
		t.Fatalf("ReconcileStore: %v", err)
	}
	CurrentStore().Record(Entry{QName: "a.home.arpa."})

	if err := CloseStore(); err != nil {
		t.Fatalf("CloseStore: %v", err)
	}
	if CurrentStore() != nil {
		t.Fatal("expected CurrentStore() to be nil after CloseStore")
	}

	db := reopenForVerification(t, dbPath)
	var got int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM queries`).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != 1 {
		t.Fatalf("row count after CloseStore = %d, want 1 (graceful close should flush buffered entries)", got)
	}
}
