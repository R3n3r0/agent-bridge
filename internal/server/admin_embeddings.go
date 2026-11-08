package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/r3n3r0/agent-bridge/internal/embeddings"
)

// embeddingRoutes registers the API of the embedding models section of the
// admin page. Without a manager the status reports it disabled and the
// other calls answer 404.
func (s *Server) embeddingRoutes(mux *http.ServeMux) {
	mux.Handle("GET /admin/api/embeddings", s.auth(http.HandlerFunc(s.handleEmbeddingStatus)))
	mux.Handle("POST /admin/api/embeddings/pulls", s.auth(s.adminWrite(s.withEmbeddings(s.handleEmbeddingPull))))
	mux.Handle("DELETE /admin/api/embeddings/pulls", s.auth(s.adminWrite(s.withEmbeddings(s.handleEmbeddingCancel))))
	mux.Handle("DELETE /admin/api/embeddings/models", s.auth(s.adminWrite(s.withEmbeddings(s.handleEmbeddingDelete))))
	mux.Handle("POST /admin/api/embeddings/test", s.auth(s.adminWrite(s.withEmbeddings(s.handleEmbeddingTest))))
}

func (s *Server) withEmbeddings(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.embed == nil {
			writeError(w, newError(http.StatusNotFound, errInvalidRequest, "embedding models are not enabled (ollama_url)"))
			return
		}
		next(w, r)
	}
}

func (s *Server) handleEmbeddingStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.embed == nil {
		writeJSON(w, http.StatusOK, embeddings.Status{Models: []embeddings.Model{}, Pulls: []embeddings.Pull{}})
		return
	}
	writeJSON(w, http.StatusOK, s.embed.Status(r.Context()))
}

// modelBody reads {"model": "...", "text": "..."}.
func modelBody(w http.ResponseWriter, r *http.Request) (model, text string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body struct {
		Model string `json:"model"`
		Text  string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, newError(http.StatusBadRequest, errInvalidRequest, "body must be {\"model\": \"...\"}"))
		return "", "", false
	}
	return body.Model, body.Text, true
}

func (s *Server) handleEmbeddingPull(w http.ResponseWriter, r *http.Request) {
	model, _, ok := modelBody(w, r)
	if !ok {
		return
	}
	p, err := s.embed.Pull(model)
	if err != nil {
		writeError(w, embeddingError(err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleEmbeddingCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.embed.CancelPull(r.URL.Query().Get("model")); err != nil {
		writeError(w, embeddingError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEmbeddingDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.embed.Delete(r.Context(), r.URL.Query().Get("model")); err != nil {
		writeError(w, embeddingError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEmbeddingTest(w http.ResponseWriter, r *http.Request) {
	model, text, ok := modelBody(w, r)
	if !ok {
		return
	}
	if text == "" {
		text = "agent-bridge embedding test"
	}
	res, err := s.embed.Test(r.Context(), model, text)
	if err != nil {
		writeError(w, embeddingError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func embeddingError(err error) *apiError {
	switch {
	case errors.Is(err, embeddings.ErrBadModel), errors.Is(err, embeddings.ErrRejected):
		return newError(http.StatusBadRequest, errInvalidRequest, err.Error())
	case errors.Is(err, embeddings.ErrNotFound):
		return newError(http.StatusNotFound, errInvalidRequest, err.Error())
	case errors.Is(err, embeddings.ErrNoPull):
		return newError(http.StatusConflict, errInvalidRequest, err.Error())
	case errors.Is(err, embeddings.ErrUnavailable):
		return newError(http.StatusBadGateway, errServerError, err.Error())
	}
	return newError(http.StatusInternalServerError, errServerError, err.Error())
}
