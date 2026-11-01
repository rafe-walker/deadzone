package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

func history() model.History {
	h := model.History{At: time.Date(2026, 10, 31, 16, 59, 30, 0, time.UTC), StepS: 5}
	for i := 0; i < 180; i++ {
		h.Drop = append(h.Drop, model.F(0))
		h.LatMs = append(h.LatMs, model.F(40+float64(i%10)))
	}
	h.LatMs[3] = nil
	return h
}

func TestStateIncludesHistory15m(t *testing.T) {
	h := NewHandler(Deps{State: state, History: history, Version: "t"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/state", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	var doc struct {
		Verdict   model.Verdict  `json:"verdict"`
		History   *model.History `json:"history_15m"`
		Power     model.Power    `json:"power"`
		Timestamp time.Time      `json:"ts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Verdict.CallOK != model.Amber || *doc.Power.SOCPct != 42 || doc.Timestamp.IsZero() {
		t.Fatalf("state fields lost next to history: %+v", doc)
	}
	if doc.History == nil || len(doc.History.Drop) != 180 || doc.History.StepS != 5 {
		t.Fatalf("history_15m missing or wrong: %+v", doc.History)
	}
	if doc.History.LatMs[3] != nil {
		t.Fatal("nil bucket must round-trip as null")
	}
	if !strings.Contains(rr.Body.String(), `"lat_ms_p90"`) {
		t.Fatal("history key names changed")
	}
}

func TestStateOmitsHistoryWhenAbsent(t *testing.T) {
	for name, deps := range map[string]Deps{
		"no history func": {State: state, Version: "t"},
		"empty history":   {State: state, History: func() model.History { return model.History{} }, Version: "t"},
	} {
		h := NewHandler(deps)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/state", nil))
		if strings.Contains(rr.Body.String(), "history_15m") {
			t.Fatalf("%s: history_15m should be omitted", name)
		}
	}
	// The MQTT contract type must not have grown the field.
	b, _ := json.Marshal(state())
	if strings.Contains(string(b), "history_15m") {
		t.Fatal("model.State must not carry history_15m (retained MQTT size)")
	}
}

func TestManifestAndIconsServed(t *testing.T) {
	h := Handler(state, "t")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	var m struct {
		Name     string `json:"name"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Src, Sizes, Type string
		} `json:"icons"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "deadzone" || m.Display != "standalone" || len(m.Icons) != 2 {
		t.Fatalf("manifest fields: %+v", m)
	}
	// Relative so HA ingress sub-paths work.
	if strings.HasPrefix(m.StartURL, "/") || strings.HasPrefix(m.Scope, "/") {
		t.Fatalf("start_url/scope must be relative: %q %q", m.StartURL, m.Scope)
	}
	for _, ic := range m.Icons {
		if strings.HasPrefix(ic.Src, "/") || ic.Type != "image/png" {
			t.Fatalf("icon %+v", ic)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/"+ic.Src, nil))
		if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%s: %d %q", ic.Src, rr.Code, rr.Header().Get("Content-Type"))
		}
		if !strings.HasPrefix(rr.Body.String(), "\x89PNG") {
			t.Fatalf("%s is not a PNG", ic.Src)
		}
	}
}

func TestIndexBudgetAndFeatures(t *testing.T) {
	h := Handler(state, "t")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	if n := len(body); n > 40*1024 {
		t.Fatalf("index.html is %d bytes; budget is 40 KB", n)
	}
	for _, want := range []string{
		`rel="manifest" href="manifest.webmanifest"`, // PWA install, relative
		`id="cSA"`, `id="cSL"`, `id="cFC"`, // per-source data-age chips
		`id="strip"`,                     // next-6h strip
		`history_15m`, `createElementNS`, // inline-SVG sparkline from the API history
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	for _, banned := range []string{"<script src=", "cdn.", "unpkg", "jsdelivr"} {
		if strings.Contains(body, banned) {
			t.Errorf("index.html must not load external scripts (%q)", banned)
		}
	}
}
