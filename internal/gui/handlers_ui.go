package gui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/mallardduck/BrambleGate/internal/gui/ui"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsadvertise"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/plugins/querylog"
)

// render writes page as the full document (wrapped in ui.Base) for a normal
// navigation, or as a bare fragment for an htmx request — htmx swaps the
// fragment into #content, so it must not repeat the surrounding chrome.
func render(w http.ResponseWriter, r *http.Request, title, active string, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = page.Render(r.Context(), w)
		return
	}
	ctx := templ.WithChildren(r.Context(), page)
	_ = ui.Base(title, active).Render(ctx, w)
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
	render(w, r, "Dashboard", ui.PathDashboard, ui.Dashboard(data))
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
	data := ui.RecordsData{Records: rs, VLANs: settings.VLANs, Editing: editing}
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

func (h *handlers) queryLogPage(w http.ResponseWriter, r *http.Request) {
	h.renderQueryLog(w, r, querylog.Filter{})
}

// queryLogGridFragment serves the auto-refresh/filter poll: just the table
// body's rows plus the out-of-band count (see ui.QueryLogGrid), mirroring
// mdnsGridFragment's shape. Unlike mDNS's client-side filtering,
// querylog.Ring.Snapshot already accepts a Filter directly, so there's no
// separate filterQueryLogEntries helper needed.
func (h *handlers) queryLogGridFragment(w http.ResponseWriter, r *http.Request) {
	entries := queryLogSnapshot(queryLogFilterFromRequest(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ui.QueryLogGrid(entries).Render(r.Context(), w)
}

func (h *handlers) renderQueryLog(w http.ResponseWriter, r *http.Request, f querylog.Filter) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.QueryLogData{Enabled: settings.QueryLog.Enabled, Filter: f}
	if settings.QueryLog.Enabled {
		data.Entries = queryLogSnapshot(f)
	}
	render(w, r, "Query Log", ui.PathQueryLog, ui.QueryLog(data))
}

func queryLogFilterFromRequest(r *http.Request) querylog.Filter {
	return querylog.Filter{
		QName:  strings.TrimSpace(r.URL.Query().Get("q")),
		Client: strings.TrimSpace(r.URL.Query().Get("client")),
		VLAN:   strings.TrimSpace(r.URL.Query().Get("vlan")),
	}
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
