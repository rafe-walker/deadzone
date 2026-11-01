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

// HistoryFunc returns the compact 15-minute dish history for the page.
type HistoryFunc func() model.History

// Deps are the handler's inputs. History is optional (nil when no dish is
// configured); the page then draws no sparkline.
type Deps struct {
	State   StateFunc
	History HistoryFunc
	Version string
}

// stateDoc is what GET /api/state serves: the MQTT state document plus
// history_15m, which is HTTP-only so the retained MQTT message stays small.
type stateDoc struct {
	model.State
	History15m *model.History `json:"history_15m,omitempty"`
}

// Handler builds the HTTP mux without history (kept for callers that only
// have a state function).
func Handler(state StateFunc, version string) http.Handler {
	return NewHandler(Deps{State: state, Version: version})
}

// NewHandler builds the HTTP mux.
//
//	GET /               embedded dashboard
//	GET /manifest.webmanifest, /icon-192.png, /icon-512.png   PWA install
//	GET /api/state      State JSON (deadzone/state) + history_15m
//	GET /api/v1/state.json  alias
//	GET /api/verdict    just the verdict block
//	GET /healthz        "ok"
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()
	index := mustAsset("index.html")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
	mux.HandleFunc("GET /index.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "./", http.StatusFound)
	})
	for _, a := range []struct{ name, ctype string }{
		{"manifest.webmanifest", "application/manifest+json"},
		{"icon-192.png", "image/png"},
		{"icon-512.png", "image/png"},
	} {
		body, ctype := mustAsset(a.name), a.ctype
		mux.HandleFunc("GET /"+a.name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = w.Write(body)
		})
	}
	stateHandler := func(w http.ResponseWriter, r *http.Request) {
		doc := stateDoc{State: d.State()}
		if d.History != nil {
			h := d.History()
			if len(h.Drop) > 0 {
				doc.History15m = &h
			}
		}
		writeJSON(w, doc)
	}
	mux.HandleFunc("GET /api/state", stateHandler)
	mux.HandleFunc("GET /api/v1/state.json", stateHandler)
	mux.HandleFunc("GET /api/verdict", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.State().Verdict)
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": d.Version})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	return noStore(mux)
}

func mustAsset(name string) []byte {
	b, err := fs.ReadFile(web.FS, name)
	if err != nil {
		panic("web/" + name + " not embedded: " + err.Error())
	}
	return b
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
