package gui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/internal/configgen"
	"github.com/mallardduck/BrambleGate/internal/gui/ui"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsadvertise"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/plugins/querylog"
)

// render writes page as the full document (wrapped in ui.Base) for a normal
// navigation, or as a bare fragment for an htmx request — htmx swaps the
// fragment into #content, so it must not repeat the surrounding chrome.
//
// extraHead is page-specific <head> content (e.g. the Dashboard's Chart.js
// tags) — variadic purely so every existing render(...) call site compiles
// unchanged; at most the first value is used.
func render(w http.ResponseWriter, r *http.Request, title, active string, page templ.Component, extraHead ...templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = page.Render(r.Context(), w)
		return
	}
	var head templ.Component
	if len(extraHead) > 0 {
		head = extraHead[0]
	}
	ctx := templ.WithChildren(r.Context(), page)
	_ = ui.Base(title, active, head).Render(ctx, w)
}

// renderError is render plus an out-of-band error toast (see ui.Toast):
// pages no longer carry their own inline "FormError"/"Error" text, so this
// is the one place any handler flashes a user action's failure, regardless
// of which page it happened on.
func renderError(w http.ResponseWriter, r *http.Request, title, active string, page templ.Component, errMsg string) {
	if errMsg != "" {
		page = templ.Join(page, ui.Toast(errMsg))
	}
	render(w, r, title, active, page)
}

// --- Dashboard ---------------------------------------------------------

// dashboardStatsWindow/dashboardTopN are the Activity section's fixed
// top-N lookback and size (dev-docs/roadmap.md's Phase 7c defaults) — not
// yet user-configurable; see query-log.md's Phase 7c design notes on
// revisiting this once a GUI control is worth adding.
const (
	dashboardStatsWindow = 24 * time.Hour
	dashboardTopN        = 10

	// dashboardClientActivityTopN/Bucket size the Client Activity stacked
	// bar chart — a smaller top-N than dashboardTopN's tables since each
	// one is a chart legend entry/color, not a table row (dataviz skill:
	// stay within the validated 8-color categorical palette), and an hourly
	// bucket over dashboardStatsWindow gives 24 bars, readable where the
	// in-memory rollup's 10-minute buckets (RecentSeries) would be too fine.
	dashboardClientActivityTopN   = 5
	dashboardClientActivityBucket = time.Hour
)

func (h *handlers) dashboardPage(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rs, err := h.svc.Records()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mdnsCount := 0
	if settings.MDNS.Enabled {
		if entries, err := h.svc.MDNSCandidates(); err == nil {
			mdnsCount = len(entries)
		}
	}
	// Non-fatal: the auto-detected panel is a nice-to-have, not core to the
	// dashboard loading.
	selfRecords, _ := h.svc.ACMESelfRecords()
	data := ui.DashboardData{
		Settings:        settings,
		RecordCount:     len(rs.Records),
		MDNSCount:       mdnsCount,
		Cert:            h.svc.ACMEStatus(),
		ACMESelfRecords: selfRecords,
	}
	var extraHead templ.Component
	if settings.QueryLog.Enabled {
		data.Activity = dashboardActivityData(r)
		extraHead = ui.DashboardExtraHead()
	}
	render(w, r, "Dashboard", ui.PathDashboard, ui.Dashboard(data), extraHead)
}

// dashboardActivityFragment serves the Activity section's 60s poll (see
// ui.DashboardActivity's hx-trigger) — a plain fragment response, same
// shape as queryLogGridFragment.
func (h *handlers) dashboardActivityFragment(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !settings.QueryLog.Enabled {
		_ = ui.Dashboard(ui.DashboardData{Settings: settings}).Render(r.Context(), w)
		return
	}
	_ = ui.DashboardActivity(dashboardActivityData(r)).Render(r.Context(), w)
}

// queryLogStats is a debug/troubleshooting surface: the exact same
// in-memory rollup Totals the Dashboard's Activity tiles/charts render
// (dashboardActivityData below), exposed as raw JSON — so a discrepancy
// between what a chart shows and what the underlying counters actually hold
// can be checked directly (e.g. `curl .../api/querylog/stats`) without
// having to trust chart rendering/legend reading. The UI itself never calls
// this; it's for humans, mirroring listPlugins' same rationale.
func (h *handlers) queryLogStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, querylog.CurrentLog().Totals())
}

// dashboardActivityData reads querylog.CurrentLog() for the Dashboard's
// Activity section. TopDomains/TopClients share one underlying
// "is Store configured" check (Log.TopDomains/TopClients, plugins/querylog/
// log.go) — a failed TopDomains lookup means TopClients would fail the same
// way, so StoreConfigured is set from the first and TopClients is only
// attempted when it succeeded, rather than treating the two as independent
// failures.
func dashboardActivityData(r *http.Request) ui.DashboardActivityData {
	log := querylog.CurrentLog()
	data := ui.DashboardActivityData{
		Totals: log.Totals(),
		Series: log.RecentSeries(),
	}
	top, err := log.TopDomains(r.Context(), dashboardStatsWindow, dashboardTopN)
	if err != nil {
		return data
	}
	data.StoreConfigured = true
	data.TopDomains = top
	data.TopClients, _ = log.TopClients(r.Context(), dashboardStatsWindow, dashboardTopN)
	now := time.Now()
	data.ClientActivity, _ = log.ClientActivity(r.Context(), now.Add(-dashboardStatsWindow), now, dashboardClientActivityBucket, dashboardClientActivityTopN)
	return data
}

// --- Records -------------------------------------------------------------

func (h *handlers) recordsPage(w http.ResponseWriter, r *http.Request) {
	h.renderRecords(w, r, nil, "")
}

func (h *handlers) recordsEdit(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	rtype := model.RecordType(strings.ToUpper(chi.URLParam(r, "type")))
	rs, err := h.svc.Records()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, ok := findRecord(rs, name, rtype)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rec.IsMDNS() {
		h.renderRecords(w, r, nil, "live mDNS-linked records are managed from the mDNS page, not edited here")
		return
	}
	h.renderRecords(w, r, &rec, "")
}

func (h *handlers) recordsCreate(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, err := parseRecordForm(r, settings.VLANs)
	if err != nil {
		h.renderRecords(w, r, nil, err.Error())
		return
	}
	if err := h.svc.AddRecord(rec); err != nil {
		h.renderRecords(w, r, nil, err.Error())
		return
	}
	h.renderRecords(w, r, nil, "")
}

func (h *handlers) recordsUpdate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	rtype := model.RecordType(strings.ToUpper(chi.URLParam(r, "type")))
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rs, err := h.svc.Records()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing, ok := findRecord(rs, name, rtype); ok && existing.IsMDNS() {
		h.renderRecords(w, r, nil, "live mDNS-linked records are managed from the mDNS page, not edited here")
		return
	}
	rec, err := parseRecordForm(r, settings.VLANs)
	if err != nil {
		h.renderRecordsAtIdentity(w, r, name, rtype, err.Error())
		return
	}
	if err := h.svc.UpdateRecord(name, rtype, rec); err != nil {
		h.renderRecordsAtIdentity(w, r, name, rtype, err.Error())
		return
	}
	h.renderRecords(w, r, nil, "")
}

func (h *handlers) recordsDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	rtype := model.RecordType(strings.ToUpper(chi.URLParam(r, "type")))
	errMsg := ""
	if err := h.svc.DeleteRecord(name, rtype); err != nil {
		errMsg = err.Error()
	}
	h.renderRecords(w, r, nil, errMsg)
}

// renderRecords renders the records page with the current on-disk record set.
// editing, when non-nil, pre-fills the form for an update instead of an add.
func (h *handlers) renderRecords(w http.ResponseWriter, r *http.Request, editing *model.Record, formErr string) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rs, err := h.svc.Records()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.RecordsData{
		Records:     rs,
		VLANs:       settings.VLANs,
		Editing:     editing,
		Zones:       configgen.OwnedZones(settings),
		Fallthrough: configgen.FallthroughZones(settings),
	}
	renderError(w, r, "Records", ui.PathRecords, ui.Records(data), formErr)
}

// renderRecordsAtIdentity re-renders the form in edit mode for name/type (the
// on-disk values, not the failed submission) alongside formErr, used when an
// update attempt fails validation.
func (h *handlers) renderRecordsAtIdentity(w http.ResponseWriter, r *http.Request, name string, rtype model.RecordType, formErr string) {
	rs, err := h.svc.Records()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec, ok := findRecord(rs, name, rtype)
	if !ok {
		h.renderRecords(w, r, nil, formErr)
		return
	}
	h.renderRecords(w, r, &rec, formErr)
}

func findRecord(rs model.RecordSet, name string, rtype model.RecordType) (model.Record, bool) {
	target := model.Record{Name: name}.NormalizedName()
	for _, rec := range rs.Records {
		if rec.NormalizedName() == target && strings.EqualFold(string(rec.Type), string(rtype)) {
			return rec, true
		}
	}
	return model.Record{}, false
}

// parseRecordForm reads the add/edit record form, including one set of
// override_{nx,value,ttl}_<vlan> fields per VLAN currently configured.
func parseRecordForm(r *http.Request, vlans []model.VLAN) (model.Record, error) {
	if err := r.ParseForm(); err != nil {
		return model.Record{}, err
	}
	rec := model.Record{
		Name:    strings.TrimSpace(r.FormValue("name")),
		Type:    model.RecordType(strings.ToUpper(r.FormValue("type"))),
		Default: strings.TrimSpace(r.FormValue("default")),
	}
	if ttl, err := strconv.Atoi(r.FormValue("ttl")); err == nil && ttl > 0 {
		rec.TTL = uint32(ttl)
	}
	for _, v := range vlans {
		nx := r.FormValue("override_nx_"+v.Name) != ""
		val := strings.TrimSpace(r.FormValue("override_value_" + v.Name))
		ttl, _ := strconv.Atoi(r.FormValue("override_ttl_" + v.Name))
		switch {
		case nx:
			rec.VLANOverrides = append(rec.VLANOverrides, model.VLANOverride{VLAN: v.Name, NXDomain: true})
		case val != "" || ttl > 0:
			ov := model.VLANOverride{VLAN: v.Name, Value: val}
			if ttl > 0 {
				ov.TTL = uint32(ttl)
			}
			rec.VLANOverrides = append(rec.VLANOverrides, ov)
		}
	}
	return rec, nil
}

// --- Hosts -----------------------------------------------------------------

func (h *handlers) hostsPage(w http.ResponseWriter, r *http.Request) {
	h.renderHosts(w, r, nil, "")
}

func (h *handlers) hostsEdit(w http.ResponseWriter, r *http.Request) {
	hostname := chi.URLParam(r, "hostname")
	hs, err := h.svc.Hosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host, ok := findHost(hs, hostname)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.renderHosts(w, r, &host, "")
}

func (h *handlers) hostsCreate(w http.ResponseWriter, r *http.Request) {
	host, err := parseHostForm(r)
	if err != nil {
		h.renderHosts(w, r, nil, err.Error())
		return
	}
	warnings, err := h.svc.AddHost(host)
	if err != nil {
		h.renderHosts(w, r, nil, err.Error())
		return
	}
	h.renderHostsWithWarnings(w, r, nil, "", warnings)
}

func (h *handlers) hostsUpdate(w http.ResponseWriter, r *http.Request) {
	hostname := chi.URLParam(r, "hostname")
	host, err := parseHostForm(r)
	if err != nil {
		h.renderHostsAtIdentity(w, r, hostname, err.Error())
		return
	}
	warnings, err := h.svc.UpdateHost(hostname, host)
	if err != nil {
		h.renderHostsAtIdentity(w, r, hostname, err.Error())
		return
	}
	h.renderHostsWithWarnings(w, r, nil, "", warnings)
}

func (h *handlers) hostsDelete(w http.ResponseWriter, r *http.Request) {
	hostname := chi.URLParam(r, "hostname")
	warnings, err := h.svc.DeleteHost(hostname)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	h.renderHostsWithWarnings(w, r, nil, errMsg, warnings)
}

// renderHosts renders the hosts page with the current on-disk host set.
// editing, when non-nil, pre-fills the form for an update instead of an add.
func (h *handlers) renderHosts(w http.ResponseWriter, r *http.Request, editing *model.Host, formErr string) {
	h.renderHostsWithWarnings(w, r, editing, formErr, nil)
}

// renderHostsWithWarnings is renderHosts plus configgen's non-fatal render
// warnings (e.g. a hosts entry shadowing a records.yaml name) — surfaced the
// same way as a form error (a toast), just not blocking the save that
// triggered it (dev-docs/static-hosts.md's "precedence consequence" note).
func (h *handlers) renderHostsWithWarnings(w http.ResponseWriter, r *http.Request, editing *model.Host, formErr string, warnings []string) {
	hs, err := h.svc.Hosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.HostsData{Hosts: hs, Editing: editing}
	msg := formErr
	if len(warnings) > 0 {
		warnMsg := "Warning: " + strings.Join(warnings, "; ")
		if msg == "" {
			msg = warnMsg
		} else {
			msg += "; " + warnMsg
		}
	}
	renderError(w, r, "Hosts", ui.PathHosts, ui.Hosts(data), msg)
}

// renderHostsAtIdentity re-renders the form in edit mode for hostname (the
// on-disk values, not the failed submission) alongside formErr — mirrors
// renderRecordsAtIdentity.
func (h *handlers) renderHostsAtIdentity(w http.ResponseWriter, r *http.Request, hostname string, formErr string) {
	hs, err := h.svc.Hosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host, ok := findHost(hs, hostname)
	if !ok {
		h.renderHosts(w, r, nil, formErr)
		return
	}
	h.renderHosts(w, r, &host, formErr)
}

func findHost(hs model.HostSet, hostname string) (model.Host, bool) {
	target := model.NormalizedHostName(hostname)
	for _, host := range hs.Hosts {
		if model.NormalizedHostName(host.Hostname) == target {
			return host, true
		}
	}
	return model.Host{}, false
}

// parseHostForm reads the add/edit hosts form: ip, hostname, and a
// comma-separated aliases field.
func parseHostForm(r *http.Request) (model.Host, error) {
	if err := r.ParseForm(); err != nil {
		return model.Host{}, err
	}
	host := model.Host{
		IP:       strings.TrimSpace(r.FormValue("ip")),
		Hostname: strings.TrimSpace(r.FormValue("hostname")),
	}
	for _, a := range strings.Split(r.FormValue("aliases"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			host.Aliases = append(host.Aliases, a)
		}
	}
	return host, nil
}

// --- Settings --------------------------------------------------------------

func (h *handlers) settingsPage(w http.ResponseWriter, r *http.Request) {
	h.renderSettings(w, r, nil, "")
}

func (h *handlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := parseSettingsForm(r, &settings); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	h.renderSettings(w, r, nil, "")
}

func (h *handlers) vlanAdd(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("vlan_name"))
	cidrs := splitAndTrim(r.FormValue("vlan_cidrs"))
	if name == "" || len(cidrs) == 0 {
		h.renderSettings(w, r, nil, "a VLAN needs a name and at least one CIDR")
		return
	}
	settings.VLANs = append(settings.VLANs, model.VLAN{Name: name, CIDRs: cidrs})
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	h.renderSettings(w, r, nil, "")
}

func (h *handlers) vlanEdit(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, v := range settings.VLANs {
		if v.Name == name {
			h.renderSettings(w, r, &v, "")
			return
		}
	}
	http.NotFound(w, r)
}

func (h *handlers) vlanUpdate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	newName := strings.TrimSpace(r.FormValue("vlan_name"))
	cidrs := splitAndTrim(r.FormValue("vlan_cidrs"))
	if newName == "" || len(cidrs) == 0 {
		h.renderSettings(w, r, nil, "a VLAN needs a name and at least one CIDR")
		return
	}
	found := false
	for i, v := range settings.VLANs {
		if v.Name == name {
			settings.VLANs[i] = model.VLAN{Name: newName, CIDRs: cidrs}
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	h.renderSettings(w, r, nil, "")
}

func (h *handlers) vlanRemove(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	kept := settings.VLANs[:0]
	for _, v := range settings.VLANs {
		if v.Name != name {
			kept = append(kept, v)
		}
	}
	settings.VLANs = kept
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, nil, err.Error())
		return
	}
	h.renderSettings(w, r, nil, "")
}

// renderSettings renders the settings page with the current on-disk
// settings. editing, when non-nil, pre-fills the Add-VLAN form for an
// update instead of an add — mirrors renderRecords's editing param.
func (h *handlers) renderSettings(w http.ResponseWriter, r *http.Request, editing *model.VLAN, formErr string) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Non-fatal: the detected-networks panel is a nice-to-have, not core to
	// the settings page loading.
	candidates, _ := h.svc.VLANCandidates()
	data := ui.SettingsData{
		Settings:           settings,
		VLANCandidates:     candidates,
		Editing:            editing,
		AdvertisedServices: mdnsadvertise.DesiredServices(settings),
	}
	renderError(w, r, "Settings", ui.PathSettings, ui.Settings(data), formErr)
}

func parseSettingsForm(r *http.Request, s *model.Settings) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	s.UpstreamDNS.Address = strings.TrimSpace(r.FormValue("upstream_address"))
	s.UpstreamDNS.Protocol = r.FormValue("upstream_protocol")
	s.UpstreamDNS.ECS = r.FormValue("upstream_ecs_enabled") != ""
	parseUintPtr(r, "upstream_max_fails", &s.UpstreamDNS.MaxFails)
	parseUint32(r, "upstream_health_check_seconds", &s.UpstreamDNS.HealthCheckSeconds)
	parseUint32(r, "upstream_expire_seconds", &s.UpstreamDNS.ExpireSeconds)
	parseUint32(r, "upstream_max_concurrent", &s.UpstreamDNS.MaxConcurrent)
	s.UpstreamDNS.PreferUDP = r.FormValue("upstream_prefer_udp") != ""

	s.Cache.ServeStaleDisabled = r.FormValue("cache_serve_stale_disabled") != ""
	s.Cache.PrefetchDisabled = r.FormValue("cache_prefetch_disabled") != ""
	s.Log.Disabled = r.FormValue("log_disabled") != ""
	s.Log.Classes = splitAndTrim(r.FormValue("log_classes"))
	s.Errors.ConsolidateDisabled = r.FormValue("errors_consolidate_disabled") != ""
	s.BufsizeDisabled = r.FormValue("bufsize_disabled") != ""
	s.Observability.Health = r.FormValue("observability_health") != ""
	s.Observability.Ready = r.FormValue("observability_ready") != ""
	s.Observability.Prometheus = r.FormValue("observability_prometheus") != ""

	s.QueryLog.Enabled = r.FormValue("querylog_enabled") != ""
	if v, err := strconv.Atoi(r.FormValue("querylog_capacity")); err == nil && v > 0 {
		s.QueryLog.Capacity = v
	} else {
		s.QueryLog.Capacity = 0
	}

	parseListener(r, "plain", &s.Listeners.Plain)
	parseListener(r, "dot", &s.Listeners.DoT)
	parseListener(r, "doh", &s.Listeners.DoH)
	parseListener(r, "doq", &s.Listeners.DoQ.Listener)
	if v, err := strconv.Atoi(r.FormValue("doq_max_streams")); err == nil && v >= 0 {
		s.Listeners.DoQ.MaxStreams = v
	}
	if v, err := strconv.Atoi(r.FormValue("doq_worker_pool_size")); err == nil && v >= 0 {
		s.Listeners.DoQ.WorkerPoolSize = v
	}
	parseListener(r, "doh3", &s.Listeners.DoH3.Listener)
	if v, err := strconv.Atoi(r.FormValue("doh3_max_streams")); err == nil && v >= 0 {
		s.Listeners.DoH3.MaxStreams = v
	}

	s.ACME.Enabled = r.FormValue("acme_enabled") != ""
	s.ACME.Domain = strings.TrimSpace(r.FormValue("acme_domain"))
	s.ACME.Email = strings.TrimSpace(r.FormValue("acme_email"))
	s.ACME.DNSProvider = strings.TrimSpace(r.FormValue("acme_dns_provider"))
	s.ACME.Production = r.FormValue("acme_production") != ""
	if days, err := strconv.Atoi(r.FormValue("acme_renew_before_days")); err == nil && days > 0 {
		s.ACME.RenewBeforeDays = days
	}
	// SelfSignedFallback and ACME.Enabled are mutually exclusive (the GUI
	// hides the field whenever ACME is on, but an already-hidden checkbox
	// still submits its prior value, so it's force-cleared here too).
	s.ACME.SelfSignedFallback = !s.ACME.Enabled && r.FormValue("acme_self_signed_fallback") != ""

	s.MDNS.Enabled = r.FormValue("mdns_enabled") != ""
	s.MDNS.Interfaces = splitAndTrim(r.FormValue("mdns_interfaces"))
	// mdns_service_types_mode is an explicit choice (none/default/all/custom)
	// rather than inferring "none" from a blank text field — a blank field
	// and a field that's never been touched are otherwise indistinguishable,
	// which made it look like saving "none" silently turned into "default".
	switch r.FormValue("mdns_service_types_mode") {
	case "none":
		s.MDNS.ServiceTypes = nil
	case "default":
		s.MDNS.ServiceTypes = []string{"default"}
	case "all":
		s.MDNS.ServiceTypes = []string{"all"}
	default: // "custom"
		s.MDNS.ServiceTypes = splitAndTrim(r.FormValue("mdns_service_types"))
	}
	s.MDNS.Suffix = strings.TrimSpace(r.FormValue("mdns_suffix"))
	s.MDNS.Advertise.Enabled = r.FormValue("mdns_advertise_enabled") != ""
	return nil
}

func parseListener(r *http.Request, prefix string, l *model.Listener) {
	l.Enabled = r.FormValue(prefix+"_enabled") != ""
	if !l.Enabled {
		l.Port = 0 // disabled listeners don't need a port persisted
		return
	}
	if port, err := strconv.Atoi(r.FormValue(prefix + "_port")); err == nil && port > 0 {
		l.Port = port
	}
}

// parseUint32 sets *dst from form field name: blank clears it to 0 ("unset,
// use CoreDNS's own default"), a valid non-negative integer sets it, anything
// else is left unchanged (matches parseListener's own leave-as-is-on-bad-input
// behavior for the doq tuning fields above).
func parseUint32(r *http.Request, name string, dst *uint32) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		*dst = 0
		return
	}
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		*dst = uint32(n)
	}
}

// parseUintPtr sets *dst from form field name: blank clears it to nil
// ("unset, use CoreDNS's own default"), a valid non-negative integer
// (including explicit 0, a meaningful distinct value for max_fails) sets a
// pointer to it, anything else is left unchanged.
func parseUintPtr(r *http.Request, name string, dst **uint32) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		*dst = nil
		return
	}
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		val := uint32(n)
		*dst = &val
	}
}

func splitAndTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- mDNS --------------------------------------------------------------

func (h *handlers) mdnsPage(w http.ResponseWriter, r *http.Request) {
	h.renderMDNS(w, r)
}

func (h *handlers) mdnsPublish(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if err := h.svc.SetMDNSPublished(chi.URLParam(r, "name"), true); err != nil {
		errMsg = err.Error()
	}
	h.renderMDNSWithError(w, r, errMsg)
}

func (h *handlers) mdnsUnpublish(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if err := h.svc.SetMDNSPublished(chi.URLParam(r, "name"), false); err != nil {
		errMsg = err.Error()
	}
	h.renderMDNSWithError(w, r, errMsg)
}

func (h *handlers) mdnsPromote(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if err := h.svc.PromoteMDNS(chi.URLParam(r, "name")); err != nil {
		errMsg = err.Error()
	}
	h.renderMDNSWithError(w, r, errMsg)
}

// mdnsGridFragment serves the auto-refresh poll: just the card grid's
// children, not the whole page, so the swap doesn't disturb the heading,
// copy, or scroll position (see ui.MDNSGrid doc comment). It also backs the
// search box and status filter (see mdns.templ's #mdns-filters form), which
// htmx re-submits here via hx-include on every poll, keystroke, and filter
// change — filtering happens server-side rather than duplicating this list
// client-side in JS.
func (h *handlers) mdnsGridFragment(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var entries []mdnsbridge.Entry
	if settings.MDNS.Enabled {
		if e, err := h.svc.MDNSCandidates(); err == nil {
			entries = e
		}
	}
	total := len(entries)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := r.URL.Query().Get("status")
	filtered := q != "" || status == "served" || status == "unserved"
	entries = filterMDNSEntries(entries, q, status)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ui.MDNSGrid(entries, filtered, total).Render(r.Context(), w)
}

// filterMDNSEntries applies the search box (matched case-insensitively
// against name, host, and service) and the status filter (all/served/
// unserved, where "served" means published or promoted) from the mDNS
// page's filter form.
func filterMDNSEntries(entries []mdnsbridge.Entry, q, status string) []mdnsbridge.Entry {
	if q == "" && status != "served" && status != "unserved" {
		return entries
	}
	q = strings.ToLower(q)
	out := entries[:0:0]
	for _, e := range entries {
		if q != "" {
			hay := strings.ToLower(e.Name + " " + e.Host + " " + e.Service)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		served := e.Published || e.Promoted
		if status == "served" && !served {
			continue
		}
		if status == "unserved" && served {
			continue
		}
		out = append(out, e)
	}
	return out
}

// --- Query Log -----------------------------------------------------------

// queryLogPageSize bounds each grid page — the ring itself can hold
// thousands of entries (settings-configurable capacity); paginating the
// filtered result keeps the rendered table from growing unbounded while
// scrolled/paused (see the auto-poll-pauses-off-page-1 note below).
const queryLogPageSize = 50

func (h *handlers) queryLogPage(w http.ResponseWriter, r *http.Request) {
	h.renderQueryLog(w, r, queryLogParamsFromRequest(r))
}

// queryLogGridFragment serves the auto-refresh/filter/pagination poll: the
// table body (as a full outerHTML replacement, so its own hx-trigger can
// change when Interval/Page change — see ui.QueryLogGrid) plus out-of-band
// updates of the count and pagination controls. Unlike mDNS's client-side
// filtering, querylog.Ring.Snapshot already accepts a Filter directly, so
// there's no separate filterQueryLogEntries helper needed.
func (h *handlers) queryLogGridFragment(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.queryLogGridData(queryLogParamsFromRequest(r), settings)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ui.QueryLogGrid(data).Render(r.Context(), w)
}

func (h *handlers) renderQueryLog(w http.ResponseWriter, r *http.Request, p queryLogParams) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.QueryLogData{Enabled: settings.QueryLog.Enabled}
	if settings.QueryLog.Enabled {
		data.Grid = h.queryLogGridData(p, settings)
	}
	render(w, r, "Query Log", ui.PathQueryLog, ui.QueryLog(data))
}

func (h *handlers) queryLogGridData(p queryLogParams, settings model.Settings) ui.QueryLogGridData {
	all := queryLogSnapshot(p.Filter)
	all = filterQueryLogByListener(all, p.ListenerFilter, settings.Listeners)
	page, totalPages, pageEntries := paginateQueryLog(all, p.Page, queryLogPageSize)
	return ui.QueryLogGridData{
		Entries:        pageEntries,
		Total:          len(all),
		Page:           page,
		TotalPages:     totalPages,
		Interval:       p.Interval,
		Filter:         p.Filter,
		ListenerFilter: p.ListenerFilter,
		Listeners:      settings.Listeners,
		VLANs:          settings.VLANs,
	}
}

// filterQueryLogByListener narrows entries to those whose listener maps to
// the given friendly label (see ui.ListenerLabel) — "" means no constraint.
// Applied here rather than inside querylog.Filter itself: querylog stays
// independent of model (dev-docs/repo-layout.md), so it has no way to map
// Entry.Listener's raw ":port" to "Plain"/"DoT"/etc. — only the GUI, which
// already holds Settings.Listeners, can.
func filterQueryLogByListener(entries []querylog.Entry, label string, l model.Listeners) []querylog.Entry {
	if label == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if ui.ListenerLabel(e.Listener, l) == label {
			out = append(out, e)
		}
	}
	return out
}

// queryLogParams is the full set of live-view controls read from the
// request: filter fields plus the pagination/refresh state that decides
// what the returned grid's own auto-poll trigger looks like.
type queryLogParams struct {
	Filter querylog.Filter
	// ListenerFilter is kept separate from Filter — see
	// ui.QueryLogGridData.ListenerFilter's doc comment for why.
	ListenerFilter string
	Page           int
	Interval       string // "2s", "5s", "10s", or "off"
}

func queryLogParamsFromRequest(r *http.Request) queryLogParams {
	q := r.URL.Query()
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	interval := q.Get("interval")
	if !isValidQueryLogInterval(interval) {
		interval = "2s"
	}
	return queryLogParams{
		Filter: querylog.Filter{
			QName:  strings.TrimSpace(q.Get("q")),
			Client: strings.TrimSpace(q.Get("client")),
			VLAN:   strings.TrimSpace(q.Get("vlan")),
			QType:  queryLogQTypeFromParam(q.Get("qtype")),
		},
		ListenerFilter: strings.TrimSpace(q.Get("listener")),
		Page:           page,
		Interval:       interval,
	}
}

func isValidQueryLogInterval(s string) bool {
	switch s {
	case "2s", "5s", "10s", "off":
		return true
	}
	return false
}

func queryLogQTypeFromParam(s string) uint16 {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	t, ok := dns.StringToType[s]
	if !ok {
		return 0
	}
	return t
}

// paginateQueryLog slices all (already newest-first) into one page of at
// most pageSize entries. page is clamped into [1, totalPages] rather than
// erroring or returning empty — a stale bookmarked/typed page number should
// still show something sensible.
func paginateQueryLog[T any](all []T, page, pageSize int) (clampedPage, totalPages int, out []T) {
	total := len(all)
	totalPages = (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return page, totalPages, all[start:end]
}

// queryLogSnapshot reads directly from the process-wide ring — never through
// h.svc or the durable store — so the live page stays fast regardless of
// history size (dev-docs/query-log.md). nil when querylog hasn't loaded yet
// (not present in the rendered Corefile, or no reload since startup): treated
// as "no entries yet", not an error.
func queryLogSnapshot(f querylog.Filter) []querylog.Entry {
	ring := querylog.Current()
	if ring == nil {
		return nil
	}
	return ring.Snapshot(f)
}

func (h *handlers) renderMDNS(w http.ResponseWriter, r *http.Request) {
	h.renderMDNSWithError(w, r, "")
}

func (h *handlers) renderMDNSWithError(w http.ResponseWriter, r *http.Request, errMsg string) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.MDNSData{Enabled: settings.MDNS.Enabled}
	if settings.MDNS.Enabled {
		if e, err := h.svc.MDNSCandidates(); err == nil {
			data.Entries = e
		}
	}
	renderError(w, r, "mDNS", ui.PathMDNS, ui.MDNS(data), errMsg)
}
