package gui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mallardduck/BrambleDNS/model"
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
	})

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // embedded FS is compiled in; this cannot fail at runtime
	}
	r.Handle("/*", http.FileServer(http.FS(sub)))

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
	case err == ErrNotFound:
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
