package server

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"

	"github.com/r3n3r0/agent-bridge/internal/login"
)

// headerAdmin must be present on admin requests that change state: browsers
// cannot send it cross-origin without a CORS preflight, which the bridge
// does not grant, so other sites cannot start logins or paste codes.
const headerAdmin = "X-Agent-Bridge-Admin"

// adminUI is the admin page, a React application built from web/admin
// (npm run build) into adminui.
//
//go:embed adminui
var adminUI embed.FS

// adminCSP allows only the page's own script, styles and API.
const adminCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// adminRoutes registers the admin page and its API when login management
// is enabled.
func (s *Server) adminRoutes(mux *http.ServeMux) {
	if s.logins == nil {
		return
	}
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		serveAdminFile(w, "index.html")
	})
	mux.HandleFunc("GET /admin/assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		if file == "" || path.Base(file) != file || file == ".." {
			http.NotFound(w, r)
			return
		}
		serveAdminFile(w, "assets/"+file)
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

// serveAdminFile serves one file of the embedded admin page.
func serveAdminFile(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(adminUI, path.Join("adminui", name))
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	h := w.Header()
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Security-Policy", adminCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
