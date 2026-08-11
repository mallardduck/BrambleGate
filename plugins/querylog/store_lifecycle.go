package querylog

import (
	"fmt"
	"sync"
)

// storeMu/currentStoreVal guard the process-wide shared Store, the same
// singleton shape as current.go's Ring — but, unlike the Ring, opened and
// closed by internal/cli/internal/gui's settings-save flow
// (ReconcileStore/CloseStore below), never by this plugin's setup(). A
// Store owns a real external resource (an open file, a WAL file,
// background goroutines); its lifecycle needs to track the whole
// BrambleGate process — including reacting to Query Log being turned OFF —
// which setup() structurally cannot do, since it's only ever invoked when
// the "querylog" Corefile stanza is present. See dev-docs/query-log.md and
// dev-docs/repo-layout.md's "bramble-only lifecycle" note.
var (
	storeMu         sync.RWMutex
	currentStoreVal *Store
)

// CurrentStore returns the process-wide shared Store, or nil if persistence
// isn't currently configured (Query Log disabled, or ReconcileStore hasn't
// run yet). setup() reads this directly; nothing else in this package
// mutates it outside ReconcileStore/CloseStore.
func CurrentStore() *Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return currentStoreVal
}

// ReconcileStore opens, tunes, or closes the process-wide Store to match
// the caller's current settings. Call it with the *full* desired
// configuration every time — once at process startup (before the first
// engine.New) and again after every settings save (before or after the
// resulting reload; Store's lifecycle doesn't depend on Corefile parse
// timing, unlike Ring) — so turning Query Log off is as reachable as
// turning it on, not just skippable (dev-docs/query-log.md).
//
//   - cfg.Path == "": persistence is off — close any existing Store.
//   - cfg.Path unchanged from the current Store's path: a tuning-only
//     change (retention/max rows/flush interval) — update in place via
//     Store.SetTuning rather than reopening the database, so a settings
//     change never disrupts the open connection or buffered-but-unflushed
//     entries.
//   - cfg.Path changed (or no Store yet): close the old Store, if any, and
//     open a new one at the new path.
func ReconcileStore(cfg StoreConfig) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if cfg.Path == "" {
		if currentStoreVal != nil {
			if err := currentStoreVal.Close(); err != nil {
				return fmt.Errorf("querylog: close store: %w", err)
			}
			currentStoreVal = nil
		}
		return nil
	}

	if currentStoreVal != nil && currentStoreVal.Path() == cfg.Path {
		currentStoreVal.SetTuning(cfg.RetentionDays, cfg.MaxRows, cfg.FlushInterval)
		return nil
	}

	if currentStoreVal != nil {
		if err := currentStoreVal.Close(); err != nil {
			return fmt.Errorf("querylog: close store: %w", err)
		}
		currentStoreVal = nil
	}

	s, err := OpenStore(cfg)
	if err != nil {
		return fmt.Errorf("querylog: open store: %w", err)
	}
	currentStoreVal = s
	return nil
}

// CloseStore closes the process-wide Store, if one is open, flushing
// buffered entries first — call once, from the process's shutdown path
// (dev-docs/query-log.md's Phase 7b: a graceful exit should lose nothing,
// unlike an ungraceful crash which is only bounded by the flush interval).
// A no-op if persistence was never configured.
func CloseStore() error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if currentStoreVal == nil {
		return nil
	}
	err := currentStoreVal.Close()
	currentStoreVal = nil
	return err
}
