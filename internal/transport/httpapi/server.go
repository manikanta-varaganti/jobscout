// Package httpapi exposes the service over a local JSON REST API and serves the embedded web UI.
//
// Conventions: JSON field names are snake_case; errors are {"error":{"code","message"}};
// mutating requests need the X-Jobscout header; writes accept an Idempotency-Key header.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/service"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

const (
	// CSRFHeader must be present on every mutating request. Browsers will not add a custom header
	// to a cross-site form post, and a cross-site fetch with it triggers a CORS preflight we refuse.
	CSRFHeader = "X-Jobscout"
	actorUI    = "ui"
)

type Server struct {
	svc *service.Service
	log *slog.Logger
	ui  fs.FS
	mux *http.ServeMux
}

func New(svc *service.Service, ui fs.FS, log *slog.Logger) *Server {
	s := &Server{svc: svc, ui: ui, log: log, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the fully wrapped handler.
func (s *Server) Handler() http.Handler {
	return s.requestID(s.recoverer(s.hostGuard(s.csrf(s.mux))))
}

// ListenAndServe serves on addr (use a loopback address) until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		return err
	}
}

func (s *Server) routes() {
	m := s.mux
	// "app" lets a second launch recognise an already-running jobscout on this port.
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "app": "jobscout"})
	})
	m.HandleFunc("GET /api/v1/jobs", s.searchJobs)
	m.HandleFunc("GET /api/v1/jobs.csv", s.exportCSV)
	m.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	m.HandleFunc("POST /api/v1/jobs/{id}/status", s.setStatus)
	m.HandleFunc("POST /api/v1/jobs/{id}/tags", s.addTag)
	m.HandleFunc("DELETE /api/v1/jobs/{id}/tags/{tag}", s.removeTag)
	m.HandleFunc("POST /api/v1/jobs/{id}/notes", s.addNote)
	m.HandleFunc("GET /api/v1/events", s.events)
	m.HandleFunc("GET /api/v1/options", s.options)
	m.HandleFunc("GET /api/v1/stats", s.stats)
	m.HandleFunc("GET /api/v1/scan", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.svc.ScanStatus(r.Context())) })
	m.HandleFunc("POST /api/v1/scan", s.startScan)
	m.HandleFunc("GET /api/v1/boards", s.listBoards)
	m.HandleFunc("POST /api/v1/boards", s.addBoard)
	m.HandleFunc("PATCH /api/v1/boards/{ats}/{slug}", s.updateBoard)
	m.HandleFunc("GET /api/v1/saved-searches", s.listSaved)
	m.HandleFunc("POST /api/v1/saved-searches", s.saveSearch)
	m.HandleFunc("DELETE /api/v1/saved-searches/{id}", s.deleteSaved)
	m.HandleFunc("POST /api/v1/saved-searches/{id}/viewed", s.viewedSaved)
	if s.ui != nil {
		m.Handle("GET /", http.FileServerFS(s.ui))
	}
}

// ---- middleware ----

type ctxKey int

const logKey ctxKey = 0

func (s *Server) logger(r *http.Request) *slog.Logger {
	if l, ok := r.Context().Value(logKey).(*slog.Logger); ok {
		return l
	}
	return s.log
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

// requestID tags every request with an id (echoed in X-Request-Id and in logs).
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-Id", id)
		l := s.log.With("request_id", id)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), logKey, l)))
		l.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "dur", time.Since(start).Round(time.Microsecond))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger(r).Error("panic", "err", rec, "stack", string(debug.Stack()))
				writeErr(w, 500, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// hostGuard rejects requests whose Host is not loopback (DNS-rebinding defence) and sets security headers.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "[::1]" {
			writeErr(w, 403, "forbidden", "forbidden host")
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(CSRFHeader) != "1" {
			writeErr(w, 403, "forbidden", "missing "+CSRFHeader+" header")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // the client went away; nothing useful to do
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

// fail maps domain errors to HTTP responses.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "not_found", "not found")
	case errors.Is(err, store.ErrInvalid):
		writeErr(w, 400, "invalid", err.Error())
	case errors.Is(err, store.ErrBadQuery):
		writeErr(w, 400, "bad_query", "the search could not be parsed")
	case errors.Is(err, store.ErrBadCursor):
		writeErr(w, 400, "bad_cursor", "invalid pagination cursor")
	case errors.Is(err, service.ErrScanRunning):
		writeErr(w, 409, "conflict", err.Error())
	default:
		s.logger(r).Error("request failed", "path", r.URL.Path, "err", err)
		writeErr(w, 500, "internal", "internal error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, "invalid", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func write(r *http.Request) store.Write {
	return store.Write{Actor: actorUI, RequestKey: r.Header.Get("Idempotency-Key")}
}
