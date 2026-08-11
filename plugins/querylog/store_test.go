package querylog

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// reopenForVerification opens a fresh connection to path, independent of
// any Store — used after Store.Close() has closed the original *sql.DB, to
// confirm what actually landed on disk without touching the closed handle.
func reopenForVerification(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatalf("reopen for verification: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// waitForRowCount polls until the queries table has exactly want rows, or
// fails the test after a generous timeout — flushing happens on a
// background goroutine, so tests use a short FlushInterval and poll rather
// than sleeping a fixed guess.
func waitForRowCount(t *testing.T, s *Store, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM queries`).Scan(&got); err != nil {
			t.Fatalf("count rows: %v", err)
		}
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("row count = %d, want %d (after waiting)", got, want)
}

func openTestStore(t *testing.T, cfg StoreConfig) *Store {
	t.Helper()
	if cfg.Path == "" {
		cfg.Path = filepath.Join(t.TempDir(), "querylog.db")
	}
	s, err := OpenStore(cfg)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenStore_CreatesFileAndSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", "querylog.db")
	s := openTestStore(t, StoreConfig{Path: dbPath})

	var name string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='queries'`).Scan(&name); err != nil {
		t.Fatalf("expected queries table to exist: %v", err)
	}
}

func TestOpenStore_EmptyPath_Errors(t *testing.T) {
	if _, err := OpenStore(StoreConfig{}); err == nil {
		t.Fatal("expected an error opening a Store with no Path")
	}
}

func TestStore_Record_FlushesAsynchronously(t *testing.T) {
	s := openTestStore(t, StoreConfig{FlushInterval: 10 * time.Millisecond})

	s.Record(Entry{QName: "a.home.arpa.", Timestamp: time.Now()})
	s.Record(Entry{QName: "b.home.arpa.", Timestamp: time.Now()})

	waitForRowCount(t, s, 2)
}

func TestStore_Close_FlushesBufferedEntries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "querylog.db")
	// A long flush interval that would never fire before Close, so this
	// only passes if Close's drain path (not the ticker) delivers the
	// buffered entries.
	s := openTestStore(t, StoreConfig{Path: dbPath, FlushInterval: time.Hour})

	s.Record(Entry{QName: "a.home.arpa.", Timestamp: time.Now()})
	s.Record(Entry{QName: "b.home.arpa.", Timestamp: time.Now()})

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db := reopenForVerification(t, dbPath)
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM queries`).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != 2 {
		t.Fatalf("row count after Close = %d, want 2 (Close should flush buffered entries)", got)
	}
}

func TestStore_Record_RoundTripsFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "querylog.db")
	s := openTestStore(t, StoreConfig{Path: dbPath, FlushInterval: time.Hour})

	ts := time.Now().Truncate(time.Millisecond)
	e := Entry{
		Timestamp:         ts,
		Client:            ClientInfo{IP: "10.0.0.5", VLAN: "iot"},
		QName:             "nas.home.arpa.",
		QType:             1,
		Verdict:           "local",
		Source:            "localrecords",
		Rcode:             0,
		Latency:           1500 * time.Microsecond,
		Listener:          "0.0.0.0:53",
		Proto:             "udp",
		AuthenticatedData: true,
		AnswerType:        "A",
	}
	s.Record(e)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	db := reopenForVerification(t, dbPath)

	var (
		tsMs, latencyUS, qtype, rcode, ad int64
		clientIP, vlan, qname, verdict    string
		source, listener, proto, atype    string
	)
	row := db.QueryRow(`SELECT ts_unix_ms, client_ip, vlan, qname, qtype, verdict, source,
		rcode, latency_us, listener, proto, authenticated_data, answer_type FROM queries`)
	if err := row.Scan(&tsMs, &clientIP, &vlan, &qname, &qtype, &verdict, &source,
		&rcode, &latencyUS, &listener, &proto, &ad, &atype); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if tsMs != ts.UnixMilli() {
		t.Errorf("ts_unix_ms = %d, want %d", tsMs, ts.UnixMilli())
	}
	if clientIP != e.Client.IP || vlan != e.Client.VLAN {
		t.Errorf("client = (%q,%q), want (%q,%q)", clientIP, vlan, e.Client.IP, e.Client.VLAN)
	}
	if qname != e.QName || verdict != e.Verdict || source != e.Source {
		t.Errorf("qname/verdict/source = (%q,%q,%q), want (%q,%q,%q)", qname, verdict, source, e.QName, e.Verdict, e.Source)
	}
	if latencyUS != e.Latency.Microseconds() {
		t.Errorf("latency_us = %d, want %d", latencyUS, e.Latency.Microseconds())
	}
	if listener != e.Listener || proto != e.Proto || atype != e.AnswerType {
		t.Errorf("listener/proto/answer_type = (%q,%q,%q), want (%q,%q,%q)", listener, proto, atype, e.Listener, e.Proto, e.AnswerType)
	}
	if ad != 1 {
		t.Errorf("authenticated_data = %d, want 1", ad)
	}
}

func TestStore_PruneByAge(t *testing.T) {
	s := openTestStore(t, StoreConfig{FlushInterval: 10 * time.Millisecond})

	s.Record(Entry{QName: "old.home.arpa.", Timestamp: time.Now().AddDate(0, 0, -30)})
	s.Record(Entry{QName: "new.home.arpa.", Timestamp: time.Now()})
	waitForRowCount(t, s, 2)

	s.SetTuning(1, 0, 0) // retention: 1 day
	s.prune()

	var qname string
	if err := s.db.QueryRow(`SELECT qname FROM queries`).Scan(&qname); err != nil {
		t.Fatalf("expected exactly one surviving row: %v", err)
	}
	if qname != "new.home.arpa." {
		t.Fatalf("surviving row = %q, want %q", qname, "new.home.arpa.")
	}
}

func TestStore_PruneByMaxRows(t *testing.T) {
	s := openTestStore(t, StoreConfig{FlushInterval: 10 * time.Millisecond})

	now := time.Now()
	for i := 0; i < 5; i++ {
		s.Record(Entry{QName: "q", Timestamp: now.Add(time.Duration(i) * time.Second)})
	}
	waitForRowCount(t, s, 5)

	s.SetTuning(0, 3, 0) // max_rows: 3
	s.prune()

	waitForRowCount(t, s, 3)
}

func TestStore_SetTuning_DefaultsZeroValues(t *testing.T) {
	s := openTestStore(t, StoreConfig{})

	s.SetTuning(0, 0, 0)

	if got := s.retentionDays.Load(); got != defaultRetentionDays {
		t.Errorf("retentionDays = %d, want %d", got, defaultRetentionDays)
	}
	if got := s.maxRows.Load(); got != defaultMaxRows {
		t.Errorf("maxRows = %d, want %d", got, defaultMaxRows)
	}
	if got := time.Duration(s.flushInterval.Load()); got != defaultFlushInterval {
		t.Errorf("flushInterval = %v, want %v", got, defaultFlushInterval)
	}
}

func TestStore_RecordIsNilSafe(t *testing.T) {
	var s *Store
	s.Record(Entry{QName: "a"}) // must not panic
}

func TestStore_HistorySurvivesReopen(t *testing.T) {
	// Simulates a process restart: close the Store, then OpenStore again
	// at the same path and confirm previously flushed history is still
	// there (Phase 7b's "done when": history survives a restart).
	dbPath := filepath.Join(t.TempDir(), "querylog.db")

	s1 := openTestStore(t, StoreConfig{Path: dbPath, FlushInterval: 10 * time.Millisecond})
	s1.Record(Entry{QName: "persisted.home.arpa.", Timestamp: time.Now()})
	waitForRowCount(t, s1, 1)
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := openTestStore(t, StoreConfig{Path: dbPath, FlushInterval: time.Hour})
	var got int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM queries`).Scan(&got); err != nil {
		t.Fatalf("count rows after reopen: %v", err)
	}
	if got != 1 {
		t.Fatalf("row count after reopen = %d, want 1 (history should survive)", got)
	}
}
