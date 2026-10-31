package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

func state() model.State {
	st := model.State{TS: time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC), Version: "t"}
	st.Verdict.CallOK = model.Amber
	st.Verdict.CallReasons = []string{"uplink:starlink_unknown"}
	st.Verdict.Shed = []model.ShedItem{}
	st.Power.SOCPct = model.F(42)
	return st
}

func TestIndexServed(t *testing.T) {
	h := Handler(state, "t")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "DEADZONE") || !strings.Contains(body, "fetch('api/state'") {
		t.Fatalf("index.html content unexpected: %.200s", body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	// relative links only, so HA ingress sub-paths work
	if strings.Contains(body, "fetch('/api") || strings.Contains(body, `href="/api`) {
		t.Fatal("index.html must not use absolute /api links")
	}
}

func TestStateJSON(t *testing.T) {
	h := Handler(state, "t")
	for _, path := range []string{"/api/state", "/api/v1/state.json"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 200 {
			t.Fatalf("%s: status %d", path, rr.Code)
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cache-control %q", path, rr.Header().Get("Cache-Control"))
		}
		var st model.State
		if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if st.Verdict.CallOK != model.Amber || *st.Power.SOCPct != 42 {
			t.Fatalf("%s: lost fields %+v", path, st)
		}
	}
}

func TestVerdictVersionHealth(t *testing.T) {
	h := Handler(state, "1.2.3")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/verdict", nil))
	var v model.Verdict
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || v.CallOK != model.Amber {
		t.Fatalf("verdict: %v %+v", err, v)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/version", nil))
	if !strings.Contains(rr.Body.String(), "1.2.3") {
		t.Fatal(rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "ok" {
		t.Fatal(rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/nope", nil))
	if rr.Code != 404 {
		t.Fatalf("unknown path status %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/state", nil))
	if rr.Code != 405 {
		t.Fatalf("POST status %d", rr.Code)
	}
}

func TestServeAndShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, addr, Handler(state, "t"), slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server never came up: %v", err)
	}
	resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

func TestServeBadListen(t *testing.T) {
	if err := Serve(context.Background(), "256.256.256.256:1", Handler(state, "t"), nil); err == nil {
		t.Fatal("expected listen error")
	}
}
