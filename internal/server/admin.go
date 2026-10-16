package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/r3n3r0/agent-bridge/internal/login"
)

// headerAdmin must be present on admin requests that change state: browsers
// cannot send it cross-origin without a CORS preflight, which the bridge
// does not grant, so other sites cannot start logins or paste codes.
const headerAdmin = "X-Agent-Bridge-Admin"

//go:embed admin.html
var adminPage []byte

// adminRoutes registers the admin page and its API when login management
// is enabled.
func (s *Server) adminRoutes(mux *http.ServeMux) {
	if s.logins == nil {
		return
	}
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		_, _ = w.Write(adminPage)
	})
	mux.Handle("GET /admin/api/logins", s.auth(http.HandlerFunc(s.handleLoginStatuses)))
	mux.Handle("POST /admin/api/logins/{cli}", s.auth(s.adminWrite(s.handleLoginStart)))
	mux.Handle("POST /admin/api/logins/{cli}/input", s.auth(s.adminWrite(s.handleLoginInput)))
	mux.Handle("DELETE /admin/api/logins/{cli}", s.auth(s.adminWrite(s.handleLoginCancel)))
}

// adminWrite requires the admin header on state-changing requests.
func (s *Server) adminWrite(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerAdmin) != "1" {
			writeError(w, newError(http.StatusForbidden, errPermission, headerAdmin+": 1 is required"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	})
}

func (s *Server) handleLoginStatuses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"backend": s.cfg.Backend, "clis": s.logins.Statuses(r.Context())})
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	v, err := s.logins.Start(r.PathValue("cli"))
	if err != nil {
		writeError(w, loginError(err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleLoginInput(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, newError(http.StatusBadRequest, errInvalidRequest, "body must be {\"text\": \"...\"}"))
		return
	}
	if err := s.logins.Input(r.PathValue("cli"), body.Text); err != nil {
		writeError(w, loginError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLoginCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.logins.Cancel(r.PathValue("cli")); err != nil {
		writeError(w, loginError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func loginError(err error) *apiError {
	switch {
	case errors.Is(err, login.ErrUnknownCLI):
		return newError(http.StatusNotFound, errInvalidRequest, err.Error())
	case errors.Is(err, login.ErrNoFlow):
		return newError(http.StatusConflict, errInvalidRequest, err.Error())
	case errors.Is(err, login.ErrBadInput):
		return newError(http.StatusBadRequest, errInvalidRequest, err.Error())
	}
	return newError(http.StatusInternalServerError, errServerError, err.Error())
}
