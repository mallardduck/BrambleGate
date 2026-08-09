package gui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/mallardduck/BrambleGate/internal/gui/ui"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
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
	data := ui.DashboardData{Settings: settings, RecordCount: len(rs.Records), MDNSCount: mdnsCount}
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
	data := ui.RecordsData{Records: rs, VLANs: settings.VLANs, Editing: editing, FormError: formErr}
	render(w, r, "Records", ui.PathRecords, ui.Records(data))
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
		if rec.NormalizedName() == target && rec.Type == rtype {
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
	h.renderSettings(w, r, "")
}

func (h *handlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := parseSettingsForm(r, &settings); err != nil {
		h.renderSettings(w, r, err.Error())
		return
	}
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, err.Error())
		return
	}
	h.renderSettings(w, r, "")
}

func (h *handlers) vlanAdd(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("vlan_name"))
	cidrs := splitAndTrim(r.FormValue("vlan_cidrs"))
	if name == "" || len(cidrs) == 0 {
		h.renderSettings(w, r, "a VLAN needs a name and at least one CIDR")
		return
	}
	settings.VLANs = append(settings.VLANs, model.VLAN{Name: name, CIDRs: cidrs})
	if err := h.svc.SaveSettings(settings); err != nil {
		h.renderSettings(w, r, err.Error())
		return
	}
	h.renderSettings(w, r, "")
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
		h.renderSettings(w, r, err.Error())
		return
	}
	h.renderSettings(w, r, "")
}

func (h *handlers) renderSettings(w http.ResponseWriter, r *http.Request, formErr string) {
	settings, err := h.svc.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := ui.SettingsData{Settings: settings, FormError: formErr}
	render(w, r, "Settings", ui.PathSettings, ui.Settings(data))
}

func parseSettingsForm(r *http.Request, s *model.Settings) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	s.UpstreamDNS.Address = strings.TrimSpace(r.FormValue("upstream_address"))
	s.UpstreamDNS.Protocol = r.FormValue("upstream_protocol")

	parseListener(r, "plain", &s.Listeners.Plain)
	parseListener(r, "dot", &s.Listeners.DoT)
	parseListener(r, "doh", &s.Listeners.DoH)
	parseListener(r, "doq", &s.Listeners.DoQ)

	s.ACME.Enabled = r.FormValue("acme_enabled") != ""
	s.ACME.Domain = strings.TrimSpace(r.FormValue("acme_domain"))
	s.ACME.Email = strings.TrimSpace(r.FormValue("acme_email"))
	s.ACME.DNSProvider = strings.TrimSpace(r.FormValue("acme_dns_provider"))
	s.ACME.Production = r.FormValue("acme_production") != ""
	if days, err := strconv.Atoi(r.FormValue("acme_renew_before_days")); err == nil && days > 0 {
		s.ACME.RenewBeforeDays = days
	}

	s.MDNS.Enabled = r.FormValue("mdns_enabled") != ""
	s.MDNS.Interfaces = splitAndTrim(r.FormValue("mdns_interfaces"))
	s.MDNS.ServiceTypes = splitAndTrim(r.FormValue("mdns_service_types"))
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
	_ = h.svc.SetMDNSPublished(chi.URLParam(r, "name"), true)
	h.renderMDNS(w, r)
}

func (h *handlers) mdnsUnpublish(w http.ResponseWriter, r *http.Request) {
	_ = h.svc.SetMDNSPublished(chi.URLParam(r, "name"), false)
	h.renderMDNS(w, r)
}

func (h *handlers) mdnsPromote(w http.ResponseWriter, r *http.Request) {
	_ = h.svc.PromoteMDNS(chi.URLParam(r, "name"))
	h.renderMDNS(w, r)
}

// mdnsGridFragment serves the auto-refresh poll: just the card grid's
// children, not the whole page, so the swap doesn't disturb the heading,
// copy, or scroll position (see ui.MDNSGrid doc comment).
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ui.MDNSGrid(entries).Render(r.Context(), w)
}

func (h *handlers) renderMDNS(w http.ResponseWriter, r *http.Request) {
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
	render(w, r, "mDNS", ui.PathMDNS, ui.MDNS(data))
}
