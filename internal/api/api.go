// Package api serves the JSON state and the embedded status page.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/web"
)

// StateFunc returns the current snapshot.
type StateFunc func() model.State

// Handler builds the HTTP mux.
//
//	GET /               embedded dashboard
//	GET /api/state      full State JSON (the same document as deadzone/state)
//	GET /api/v1/state.json  alias
//	GET /api/verdict    just the verdict block
//	GET /healthz        "ok"
func Handler(state StateFunc, version string) http.Handler {
	mux := http.NewServeMux()
	index, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		panic("web/index.html not embedded: " + err.Error())
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
	mux.HandleFunc("GET /index.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "./", http.StatusFound)
	})
	stateHandler := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, state())
	}
	mux.HandleFunc("GET /api/state", stateHandler)
	mux.HandleFunc("GET /api/v1/state.json", stateHandler)
	mux.HandleFunc("GET /api/verdict", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, state().Verdict)
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": version})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	return noStore(mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// Serve runs the HTTP server until ctx is cancelled.
func Serve(ctx context.Context, listen string, h http.Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Info("http listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
