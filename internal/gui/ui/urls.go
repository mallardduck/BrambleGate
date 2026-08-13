package ui

import (
	"fmt"
	"net/url"
)

// Top-level page paths. Shared between route registration (internal/gui/server.go)
// and the nav links/redirects in this package so the two can't drift.
const (
	PathDashboard = "/"
	PathRecords   = "/records"
	PathHosts     = "/hosts"
	PathSettings  = "/settings"
	PathMDNS      = "/mdns"
	PathQueryLog  = "/querylog"
	// PathDashboardActivity is the Dashboard's Activity section's poll
	// fragment (dev-docs/query-log.md's Phase 7d) — not nested under
	// PathDashboard ("/") since that's the root path itself.
	PathDashboardActivity = "/dashboard/activity"
	// PathSettingsRestart is the Settings page's Admin actions "Restart DNS
	// engine" button target.
	PathSettingsRestart = PathSettings + "/restart"
)

// AppVersion is the running server version, set from internal/version by
// main() at startup.
var AppVersion = "dev"

// RecordPath returns the URL for a single record identified by name+type, as
// used by the edit/update/delete routes.
func RecordPath(name string, rtype string) string {
	return fmt.Sprintf("%s/%s/%s", PathRecords, url.PathEscape(name), url.PathEscape(rtype))
}

// HostPath returns the URL for a single hosts.yaml entry identified by its
// (current, on-disk) hostname, as used by the edit/update/delete routes —
// mirrors RecordPath.
func HostPath(hostname string) string {
	return fmt.Sprintf("%s/%s", PathHosts, url.PathEscape(hostname))
}

// MDNSActionPath returns the URL for an mDNS action (publish/unpublish/promote)
// on the discovery entry named by name.
func MDNSActionPath(name, action string) string {
	return fmt.Sprintf("%s/%s/%s", PathMDNS, url.PathEscape(name), action)
}
