package gui

import "embed"

// staticFiles holds the dashboard's built/vendored assets: style.css (generated
// by cmd/gentailwind from internal/gui/ui/static-src/input.css) and js/ (vendored
// htmx + the theme toggle script). The pages themselves are rendered
// server-side by internal/gui/ui, not served as static files.
//
//go:embed static
var staticFiles embed.FS
