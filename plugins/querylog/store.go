package querylog

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "turso.tech/database/tursogo"
)

// Defaults applied when a StoreConfig field is left at its zero value —
// same "0 = plugin's own default" convention as defaultCapacity.
const (
	defaultRetentionDays = 7
	defaultMaxRows       = 200_000
	defaultFlushInterval = 2 * time.Second
	storeWriteBufferSize = 2048
	storeFlushBatchSize  = 200
	storePruneInterval   = 5 * time.Minute
)

// StoreConfig configures a Store's file location, retention bounds, and
// write cadence.
type StoreConfig struct {
	Path          string
	RetentionDays int           // <=0 uses defaultRetentionDays
	MaxRows       int           // <=0 uses defaultMaxRows
	FlushInterval time.Duration // <=0 uses defaultFlushInterval
}

// Store is the durable, disposable SQLite-compatible history behind the
// in-memory Ring (dev-docs/query-log.md's Phase 7b). Writes are async and
// never on the DNS hot path: Record enqueues onto a buffered channel that a
// single background writer goroutine batches into the database on the same
// cadence the entry stream arrives, not deferred to Ring eviction or
// process exit — bounding data lost to an ungraceful crash to one flush
// interval, instead of "whatever hasn't been evicted from Ring yet" (see
// the design discussion in the Phase 7b work). A separate goroutine prunes
// by age and row count so this stays bounded, unlike an audit log.
// Modeled on Pi-hole/FTL's disposable pihole-FTL.db — safe to delete at any
// time, regenerates from empty.
//
// Deliberately NOT a source Ring is ever hydrated from: Ring stays "what
// this process has seen since it booted," full stop, so there's no path by
// which a value written back from Store into Ring could loop around and be
// persisted a second time. Anything spanning a restart reads Store
// directly (a future Phase 7c/repo concern), never through Ring.
type Store struct {
	db   *sql.DB
	path string // immutable after Open

	// retentionDays/maxRows/flushInterval are tunable at runtime (a
	// settings change shouldn't require reopening the database — see
	// reconcileStore in setup.go), so they're read by the background
	// goroutines via atomics rather than plain fields.
	retentionDays atomic.Int64
	maxRows       atomic.Int64
	flushInterval atomic.Int64 // nanoseconds

	write   chan Entry
	dropped atomic.Int64

	cancel context.CancelFunc
	done   chan struct{}
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS queries (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    ts_unix_ms         INTEGER NOT NULL,
    client_ip          TEXT NOT NULL,
    vlan               TEXT NOT NULL,
    qname              TEXT NOT NULL,
    qtype              INTEGER NOT NULL,
    verdict            TEXT NOT NULL,
    source             TEXT NOT NULL,
    rcode              INTEGER NOT NULL,
    latency_us         INTEGER NOT NULL,
    listener           TEXT NOT NULL,
    proto              TEXT NOT NULL,
    authenticated_data INTEGER NOT NULL,
    answer_type        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_queries_ts_unix_ms ON queries(ts_unix_ms);
`

// OpenStore opens (creating if needed) the SQLite-compatible database at
// cfg.Path, migrates its schema, and starts the async writer/pruner
// goroutines. Call Close when done.
func OpenStore(cfg StoreConfig) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("querylog: store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("querylog: create store dir: %w", err)
	}

	db, err := sql.Open("turso", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("querylog: open store: %w", err)
	}
	// A single native connection: this phase only writes, from one
	// A small connection pool: one writer goroutine plus a few concurrent
	// readers (Phase 7c's TopDomains/TopClients/Series, queried from GUI
	// request goroutines). WAL mode (set just below) is what makes this
	// safe — SQLite-compatible readers don't block behind the writer.
	db.SetMaxOpenConns(4)

	for _, stmt := range []string{schemaSQL, `PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("querylog: prepare store: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{
		db:     db,
		path:   cfg.Path,
		write:  make(chan Entry, storeWriteBufferSize),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.SetTuning(cfg.RetentionDays, cfg.MaxRows, cfg.FlushInterval)
	go s.run(ctx)
	return s, nil
}

// SetTuning updates retention/row-cap/flush-interval in place, without
// reopening the database or disrupting buffered-but-unflushed entries —
// callable at any time from any goroutine. Zero/negative values reset the
// corresponding field to its default.
func (s *Store) SetTuning(retentionDays, maxRows int, flushInterval time.Duration) {
	if retentionDays <= 0 {
		retentionDays = defaultRetentionDays
	}
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	if flushInterval <= 0 {
		flushInterval = defaultFlushInterval
	}
	s.retentionDays.Store(int64(retentionDays))
	s.maxRows.Store(int64(maxRows))
	s.flushInterval.Store(int64(flushInterval))
}

// Path returns the database file path Store was opened with.
func (s *Store) Path() string {
	return s.path
}

// Record enqueues e for asynchronous persistence. Never blocks the DNS hot
// path: if the write buffer is full (a sustained-overload edge case), the
// entry is dropped and counted rather than backing up the caller — this is
// disposable observational history, not an audit log (dev-docs/query-log.md).
// A nil *Store (persistence not configured) is a safe no-op.
func (s *Store) Record(e Entry) {
	if s == nil {
		return
	}
	select {
	case s.write <- e:
	default:
		s.dropped.Add(1)
	}
}

// Dropped returns the number of entries discarded so far because the write
// buffer was full.
func (s *Store) Dropped() int64 {
	return s.dropped.Load()
}

// Close stops the background goroutines, flushing any buffered entries
// (best-effort) before closing the database. Safe to call once; Store is
// not reusable after Close.
func (s *Store) Close() error {
	s.cancel()
	<-s.done
	return s.db.Close()
}

func (s *Store) run(ctx context.Context) {
	defer close(s.done)

	// Bound the store immediately after opening, e.g. a shrunk retention
	// setting taking effect right away rather than waiting a full interval.
	s.prune()

	flushTicker := time.NewTicker(s.currentFlushInterval())
	defer flushTicker.Stop()
	pruneTicker := time.NewTicker(storePruneInterval)
	defer pruneTicker.Stop()

	batch := make([]Entry, 0, storeFlushBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.insertBatch(batch); err != nil {
			slog.Warn("querylog: store write failed", "err", err, "entries", len(batch))
		}
		batch = batch[:0]
	}

	for {
		select {
		case e := <-s.write:
			batch = append(batch, e)
			if len(batch) >= storeFlushBatchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
			// FlushInterval is tunable at runtime (SetTuning) — resync the
			// ticker in case it changed since it was last read.
			flushTicker.Reset(s.currentFlushInterval())
		case <-pruneTicker.C:
			s.prune()
		case <-ctx.Done():
			s.drain(&batch)
			flush()
			return
		}
	}
}

func (s *Store) currentFlushInterval() time.Duration {
	return time.Duration(s.flushInterval.Load())
}

// drain empties any entries already sitting in the write channel
// (non-blocking) into batch, so Close doesn't silently lose queries that
// were enqueued moments before shutdown.
func (s *Store) drain(batch *[]Entry) {
	for {
		select {
		case e := <-s.write:
			*batch = append(*batch, e)
		default:
			return
		}
	}
}

const insertSQL = `INSERT INTO queries (
	ts_unix_ms, client_ip, vlan, qname, qtype, verdict, source, rcode,
	latency_us, listener, proto, authenticated_data, answer_type
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (s *Store) insertBatch(batch []Entry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range batch {
		if _, err := stmt.Exec(
			e.Timestamp.UnixMilli(), e.Client.IP, e.Client.VLAN, e.QName, e.QType,
			e.Verdict, e.Source, e.Rcode, e.Latency.Microseconds(),
			e.Listener, e.Proto, boolToInt(e.AuthenticatedData), e.AnswerType,
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prune deletes rows older than the configured retention and, if the table
// still exceeds the configured row cap, the oldest excess rows — mirrors
// Pi-hole/FTL's MAXDBDAYS (dev-docs/query-log.md), a backstop against
// sustained QPS outrunning the age-based prune before it next runs.
func (s *Store) prune() {
	cutoff := time.Now().AddDate(0, 0, -int(s.retentionDays.Load())).UnixMilli()
	if _, err := s.db.Exec(`DELETE FROM queries WHERE ts_unix_ms < ?`, cutoff); err != nil {
		slog.Warn("querylog: prune by age failed", "err", err)
		return
	}
	// Keeps the newest maxRows rows: finds the id of the row exactly
	// maxRows back from the newest, then deletes it and everything older.
	// If there are maxRows or fewer rows, the subquery returns no row and
	// "id <= NULL" is never true, so this is a no-op — no separate count
	// check needed.
	if _, err := s.db.Exec(
		`DELETE FROM queries WHERE id <= (SELECT id FROM queries ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		s.maxRows.Load(),
	); err != nil {
		slog.Warn("querylog: prune by row count failed", "err", err)
	}
}
