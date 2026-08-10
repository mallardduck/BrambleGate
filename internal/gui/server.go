package gui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/pluginreg"
)

// NewServer builds the http.Server for the dashboard + JSON API. The engine and
// this server share one process and one shutdown context (wired in internal/cli).
func NewServer(svc *Service, addr string) *http.Server {
	h := &handlers{svc: svc}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/records", h.listRecords)
		r.Post("/records", h.addRecord)
		r.Put("/records/{name}/{type}", h.updateRecord)
		r.Delete("/records/{name}/{type}", h.deleteRecord)
		r.Get("/settings", h.getSettings)
		r.Put("/settings", h.putSettings)

		r.Get("/plugins", h.listPlugins)

		r.Get("/mdns", h.listMDNS)
		r.Post("/mdns/{name}/publish", h.publishMDNS)
		r.Post("/mdns/{name}/unpublish", h.unpublishMDNS)
		r.Post("/mdns/{name}/promote", h.promoteMDNS)
	})

	// Server-rendered dashboard (internal/gui/ui): full pages on a plain GET,
	// just the inner fragment on an htmx request (see render() in handlers_ui.go).
	r.Get("/", h.dashboardPage)
	r.Get("/records", h.recordsPage)
	r.Post("/records", h.recordsCreate)
	r.Get("/records/{name}/{type}/edit", h.recordsEdit)
	r.Put("/records/{name}/{type}", h.recordsUpdate)
	r.Delete("/records/{name}/{type}", h.recordsDelete)
	r.Get("/settings", h.settingsPage)
	r.Post("/settings", h.settingsSave)
	r.Post("/settings/vlans", h.vlanAdd)
	r.Get("/settings/vlans/{name}/edit", h.vlanEdit)
	r.Put("/settings/vlans/{name}", h.vlanUpdate)
	r.Delete("/settings/vlans/{name}", h.vlanRemove)
	r.Get("/mdns", h.mdnsPage)
	r.Get("/mdns/grid", h.mdnsGridFragment)
	r.Post("/mdns/{name}/publish", h.mdnsPublish)
	r.Post("/mdns/{name}/unpublish", h.mdnsUnpublish)
	r.Post("/mdns/{name}/promote", h.mdnsPromote)

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // embedded FS is compiled in; this cannot fail at runtime
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	return &http.Server{Addr: addr, Handler: r}
}

type handlers struct{ svc *Service }

func (h *handlers) listRecords(w http.ResponseWriter, r *http.Request) {
	rs, err := h.svc.Records()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (h *handlers) addRecord(w http.ResponseWriter, r *http.Request) {
	rec, ok := decodeRecord(w, r)
	if !ok {
		return
	}
	if err := h.svc.AddRecord(rec); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (h *handlers) updateRecord(w http.ResponseWriter, r *http.Request) {
	rec, ok := decodeRecord(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	rtype := model.RecordType(strings.ToUpper(chi.URLParam(r, "type")))
	if err := h.svc.UpdateRecord(name, rtype, rec); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *handlers) deleteRecord(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	rtype := model.RecordType(strings.ToUpper(chi.URLParam(r, "type")))
	if err := h.svc.DeleteRecord(name, rtype); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Settings()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handlers) putSettings(w http.ResponseWriter, r *http.Request) {
	var s model.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody{"invalid JSON: " + err.Error()})
		return
	}
	if err := h.svc.SaveSettings(s); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// listPlugins is a debug/troubleshooting surface: every registered
// BrambleGate plugin/component (CoreDNS-chain and bramble-only alike) with its
// current loaded state and reason — see dev-docs/plugin-system.md. Unlike
// /api/mdns it needs no Service call; pluginreg is a process-wide registry.
func (h *handlers) listPlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"plugins": pluginreg.All()})
}

func (h *handlers) listMDNS(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.MDNSCandidates()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *handlers) publishMDNS(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SetMDNSPublished(chi.URLParam(r, "name"), true); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) unpublishMDNS(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SetMDNSPublished(chi.URLParam(r, "name"), false); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) promoteMDNS(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.PromoteMDNS(chi.URLParam(r, "name")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func decodeRecord(w http.ResponseWriter, r *http.Request) (model.Record, bool) {
	var rec model.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody{"invalid JSON: " + err.Error()})
		return rec, false
	}
	rec.Type = model.RecordType(strings.ToUpper(string(rec.Type)))
	return rec, true
}

type errBody struct {
	Error string `json:"error"`
}

// writeErr maps a service error to the right HTTP status: validation → 400,
// not-found → 404, everything else (including reload failures) → 500.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case IsValidation(err):
		writeJSON(w, http.StatusBadRequest, errBody{err.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody{err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, errBody{err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
