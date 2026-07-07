package gui

import "embed"

// staticFiles holds the built dashboard assets. For Phase 2 this is a single
// hand-written page (vanilla JS, no build step); Phase 6 replaces static/ with
// the output of the web/frontend npm project, embedded the same way.
//
//go:embed static
var staticFiles embed.FS
