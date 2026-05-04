package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"omp-session-viewer/backend/internal/ompstore"
)

type Server struct {
	store          *ompstore.Store
	frontendOrigin string
}

func NewServer(store *ompstore.Store, frontendOrigin string) http.Handler {
	server := &Server{store: store, frontendOrigin: frontendOrigin}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/sessions", server.listSessions)
	mux.HandleFunc("GET /api/sessions/{id}", server.getSession)
	mux.HandleFunc("GET /api/sessions/{id}/messages", server.getMessages)
	mux.HandleFunc("GET /api/cwds", server.listCWDs)
	return server.withCORS(mux)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (s.frontendOrigin == "*" || origin == s.frontendOrigin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, err := ompstore.ParseTimeQuery(query.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	to, err := ompstore.ParseTimeQuery(query.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	limit := intQuery(query.Get("limit"), 100)
	offset := intQuery(query.Get("offset"), 0)
	result, err := s.store.ListSessions(r.Context(), ompstore.ListSessionsFilter{
		Query:                query.Get("q"),
		CWD:                  query.Get("cwd"),
		SourceKind:           query.Get("sourceKind"),
		From:                 from,
		To:                   to,
		IncludeEmptyMessages: boolQuery(query.Get("includeEmptyMessages")),
		MessageCountBucket:   query.Get("messageCountBucket"),
		Limit:                limit,
		Offset:               offset,
		Sort:                 query.Get("sort"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	messages, err := s.store.GetMessages(
		r.Context(),
		r.PathValue("id"),
		strings.EqualFold(query.Get("includeRaw"), "true"),
		boolQuery(query.Get("includeToolCalls")),
		boolQuery(query.Get("includeToolResults")),
		intQuery(query.Get("limit"), 200),
		intQuery(query.Get("offset"), 0),
	)
	if err != nil {
		writeError(w, statusForStoreError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) listCWDs(w http.ResponseWriter, r *http.Request) {
	options, err := s.store.ListCWDs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": options})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func intQuery(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolQuery(value string) bool {
	return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
}

func statusForStoreError(err error) int {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, http.ErrMissingFile) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
