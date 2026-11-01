package starlink

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

func TestApplyDiagnosticsHealthy(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyDiagnostics(&s, fixture(t, "get_diagnostics.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "OKAY" {
		t.Errorf("disablement_code=%q want OKAY", s.DisablementCode)
	}
	if s.Stowed == nil || *s.Stowed {
		t.Errorf("stowed=%v want false", s.Stowed)
	}
	if s.OverageRateLimited == nil || *s.OverageRateLimited {
		t.Errorf("overage_rate_limited=%v want false", s.OverageRateLimited)
	}
	if s.HardwareSelfTest != "PASSED" {
		t.Errorf("hardware_self_test=%q", s.HardwareSelfTest)
	}
	if s.LocationEnabled == nil || *s.LocationEnabled {
		t.Errorf("location.enabled=%v want false (residential default)", s.LocationEnabled)
	}
	if !s.DiagnosticsUpdated.Equal(now) || s.DiagnosticsError != "" {
		t.Errorf("updated=%v err=%q", s.DiagnosticsUpdated, s.DiagnosticsError)
	}
	// Versions are filled from diagnostics only when status has not set them.
	if s.HardwareVersion != "rev3_proto2" {
		t.Errorf("hardware_version=%q", s.HardwareVersion)
	}
	// Diagnostics must not touch status/history fields.
	if s.Reachable || !s.Updated.IsZero() {
		t.Errorf("diagnostics changed reachability/updated: %v %v", s.Reachable, s.Updated)
	}
}

func TestApplyDiagnosticsNoAccount(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyDiagnostics(&s, fixture(t, "get_diagnostics_no_account.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "NO_ACTIVE_ACCOUNT" {
		t.Fatalf("disablement_code=%q", s.DisablementCode)
	}
	if s.LocationEnabled == nil || !*s.LocationEnabled {
		t.Errorf("location.enabled should be true in this fixture")
	}
}

func TestApplyDiagnosticsCamelStowedOverage(t *testing.T) {
	s := New(nil, Options{}).state
	s.HardwareVersion = "from_status"
	if err := ApplyDiagnostics(&s, fixture(t, "get_diagnostics_stowed_overage_camel.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "DATA_OVERAGE_SANDBOX_POLICY" {
		t.Errorf("disablement_code=%q", s.DisablementCode)
	}
	if s.Stowed == nil || !*s.Stowed {
		t.Errorf("stowed not read from camelCase")
	}
	if s.OverageRateLimited == nil || !*s.OverageRateLimited {
		t.Errorf("overageRateLimited not read from camelCase")
	}
	if s.HardwareSelfTest != "FAILED" {
		t.Errorf("hardwareSelfTest=%q", s.HardwareSelfTest)
	}
	if s.HardwareVersion != "from_status" {
		t.Errorf("status-provided hardware version must win, got %q", s.HardwareVersion)
	}
}

func TestApplyDiagnosticsRejectsOtherResponse(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyDiagnostics(&s, []byte(`{"dish_get_status":{}}`), now); err == nil || !strings.Contains(err.Error(), "dish_get_diagnostics") {
		t.Fatalf("expected a shape error, got %v", err)
	}
	if err := ApplyDiagnostics(&s, []byte(`{`), now); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestApplyStatusDisablementAndRateLimit(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyStatus(&s, fixture(t, "get_status.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "OKAY" {
		t.Errorf("status disablement_code=%q want OKAY", s.DisablementCode)
	}
	if s.RateLimitDL != "" || s.RateLimitUL != "" {
		t.Errorf("fixture has no rate-limit fields: %q %q", s.RateLimitDL, s.RateLimitUL)
	}
	raw := []byte(`{"dish_get_status":{"state":"CONNECTED","disablement_code":"DATA_OVERAGE_SANDBOX_POLICY",
	  "dl_bandwidth_restricted_reason":"OVERAGE_LIMIT","ul_bandwidth_restricted_reason":"NO_LIMIT"}}`)
	if err := ApplyStatus(&s, raw, now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "DATA_OVERAGE_SANDBOX_POLICY" || s.RateLimitDL != "OVERAGE_LIMIT" || s.RateLimitUL != "NO_LIMIT" {
		t.Errorf("got %q %q %q", s.DisablementCode, s.RateLimitDL, s.RateLimitUL)
	}
	// A status without the field (old firmware) keeps the diagnostics value.
	if err := ApplyStatus(&s, []byte(`{"dish_get_status":{"state":"CONNECTED"}}`), now); err != nil {
		t.Fatal(err)
	}
	if s.DisablementCode != "DATA_OVERAGE_SANDBOX_POLICY" {
		t.Errorf("missing field must not clear the code, got %q", s.DisablementCode)
	}
}

// diagCaller answers status from a fixture and diagnostics with a fixed error.
type diagCaller struct {
	fakeCaller
	diagErr error
	diag    int
}

func (d *diagCaller) Call(ctx context.Context, req map[string]any) ([]byte, error) {
	if _, ok := req["get_diagnostics"]; ok {
		d.diag++
		if d.diagErr != nil {
			return nil, d.diagErr
		}
	}
	return d.fakeCaller.Call(ctx, req)
}

func TestPollDiagnosticsUnsupportedDisablesLoopOnly(t *testing.T) {
	dc := &diagCaller{fakeCaller: fakeCaller{files: map[string]string{"get_status": "get_status.json"}},
		diagErr: fmt.Errorf("%w: request does not fit SpaceX.API.Device.Request: unknown field get_diagnostics", ErrReflection)}
	src := New(dc, Options{Now: func() time.Time { return now }, Logger: quiet()})
	ctx := context.Background()
	src.PollStatus(ctx)
	src.PollDiagnostics(ctx)
	src.PollDiagnostics(ctx)
	if dc.diag != 1 {
		t.Fatalf("unsupported RPC should be tried once, got %d calls", dc.diag)
	}
	if src.DiagnosticsSupported() {
		t.Fatal("should be marked unsupported")
	}
	st := src.Starlink()
	if !st.Reachable || st.Error != "" {
		t.Fatalf("diagnostics failure must not poison status: reachable=%v err=%q", st.Reachable, st.Error)
	}
	if st.DiagnosticsError == "" {
		t.Fatal("diagnostics error should be recorded for the UI")
	}
}

func TestPollDiagnosticsTransportErrorRetries(t *testing.T) {
	dc := &diagCaller{fakeCaller: fakeCaller{files: map[string]string{"get_status": "get_status.json"}},
		diagErr: fmt.Errorf("%w: connection refused", ErrUnreachable)}
	src := New(dc, Options{Now: func() time.Time { return now }, Logger: quiet()})
	ctx := context.Background()
	src.PollDiagnostics(ctx)
	src.PollDiagnostics(ctx)
	if dc.diag != 2 {
		t.Fatalf("transport errors must keep retrying, got %d calls", dc.diag)
	}
	if !src.DiagnosticsSupported() {
		t.Fatal("a transport error says nothing about support")
	}
}

func TestPollDiagnosticsMergesIntoSnapshot(t *testing.T) {
	fc := &fakeCaller{files: map[string]string{"get_status": "get_status.json", "get_diagnostics": "get_diagnostics_no_account.json"}}
	src := New(fc, Options{Now: func() time.Time { return now }, Logger: quiet()})
	ctx := context.Background()
	src.PollStatus(ctx)
	src.PollDiagnostics(ctx)
	st := src.Starlink()
	if st.DisablementCode != "NO_ACTIVE_ACCOUNT" || st.State != "CONNECTED" {
		t.Fatalf("snapshot should carry both status and diagnostics: %+v", st)
	}
}

func TestApplyHistoryPowerStats(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyHistory(&s, fixture(t, "get_history.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.PowerW == nil || *s.PowerW != 60.9 {
		t.Fatalf("power_w=%v", s.PowerW)
	}
	if s.PowerMeanW == nil || *s.PowerMeanW < 55 || *s.PowerMeanW > 65 {
		t.Fatalf("power_mean_5m_w=%v want a plausible mean of the fixture", s.PowerMeanW)
	}
	if s.PowerMinW == nil || s.PowerMaxW == nil || *s.PowerMinW > *s.PowerMeanW || *s.PowerMaxW < *s.PowerMeanW {
		t.Fatalf("min/mean/max not ordered: %v %v %v", s.PowerMinW, s.PowerMeanW, s.PowerMaxW)
	}
	if !s.HistoryUpdated.Equal(now) {
		t.Fatalf("history_updated=%v", s.HistoryUpdated)
	}
}

func TestApplyHistoryPowerAbsent(t *testing.T) {
	s := New(nil, Options{}).state
	s.PowerMeanW = model.F(70) // stale value from an earlier poll
	raw := []byte(`{"dish_get_history":{"current":"3","pop_ping_drop_rate":[0,0,0],"pop_ping_latency_ms":[30,31,32]}}`)
	if err := ApplyHistory(&s, raw, now); err != nil {
		t.Fatal(err)
	}
	if s.PowerW != nil || s.PowerMeanW != nil {
		t.Fatalf("a response without power_in must clear power, got %v %v", s.PowerW, s.PowerMeanW)
	}
}

func historyJSON(n int, drop func(i int) float64, lat func(i int) float64) []byte {
	d := make([]float64, n)
	l := make([]float64, n)
	for i := range d {
		d[i], l[i] = drop(i), lat(i)
	}
	b, _ := json.Marshal(map[string]any{"dish_get_history": map[string]any{
		"current": fmt.Sprint(n), "pop_ping_drop_rate": d, "pop_ping_latency_ms": l, "power_in": []float64{61}}})
	return b
}

// rawCaller answers every request with the same bytes.
type rawCaller struct{ raw []byte }

func (r *rawCaller) Call(context.Context, map[string]any) ([]byte, error) { return r.raw, nil }
func (r *rawCaller) Close() error                                         { return nil }

func TestHistory15mDownsamplesFullRing(t *testing.T) {
	// 900 samples: a 10 s full drop at 300..309, latency ramps 20..109 ms.
	raw := historyJSON(HistoryLen,
		func(i int) float64 {
			if i >= 300 && i < 310 {
				return 1
			}
			return 0
		},
		func(i int) float64 { return 20 + float64(i%90) })
	src := New(&rawCaller{raw: raw}, Options{Now: func() time.Time { return now }, Logger: quiet()})
	src.PollHistory(context.Background())
	s := &src.state
	hist := src.History15m()
	if len(hist.Drop) != HistoryBuckets || len(hist.LatMs) != HistoryBuckets {
		t.Fatalf("buckets=%d/%d want %d", len(hist.Drop), len(hist.LatMs), HistoryBuckets)
	}
	if hist.StepS != 5 || !hist.At.Equal(now) {
		t.Fatalf("step=%v at=%v", hist.StepS, hist.At)
	}
	// Buckets 60 and 61 (samples 300-309) are full drops with no valid latency.
	for _, b := range []int{60, 61} {
		if hist.Drop[b] == nil || *hist.Drop[b] != 1 {
			t.Errorf("bucket %d drop=%v want 1", b, hist.Drop[b])
		}
		if hist.LatMs[b] != nil {
			t.Errorf("bucket %d latency=%v want nil during a full drop", b, *hist.LatMs[b])
		}
	}
	if hist.Drop[0] == nil || *hist.Drop[0] != 0 {
		t.Errorf("bucket 0 drop=%v", hist.Drop[0])
	}
	// Bucket 0 = samples 0..4 -> latencies 20..24, p90 nearest rank of 5 = max = 24.
	if hist.LatMs[0] == nil || *hist.LatMs[0] != 24 {
		t.Errorf("bucket 0 p90=%v want 24", hist.LatMs[0])
	}
	// The 5-min gate still sees only the tail: 300 samples, none dropped.
	if s.HistorySamples != HistoryWindow || s.Drop5m == nil || *s.Drop5m != 0 {
		t.Errorf("gate window samples=%d drop5m=%v", s.HistorySamples, s.Drop5m)
	}
	// The source hands out a copy.
	got := src.History15m()
	if len(got.Drop) != HistoryBuckets {
		t.Fatalf("History15m len=%d", len(got.Drop))
	}
	got.Drop[0] = model.F(0.5)
	if *src.History15m().Drop[0] != 0 {
		t.Fatal("History15m must return a copy")
	}
}

func TestHistory15mShortRingOneSecondBuckets(t *testing.T) {
	raw := historyJSON(120, func(int) float64 { return 0.1 }, func(i int) float64 { return 40 })
	s := New(nil, Options{}).state
	hist, err := applyHistory(&s, raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Drop) != 120 || hist.StepS != 1 {
		t.Fatalf("short ring should keep 1-s resolution: %d buckets step %v", len(hist.Drop), hist.StepS)
	}
	if math.Abs(*hist.Drop[119]-0.1) > 1e-9 || *hist.LatMs[119] != 40 {
		t.Fatalf("last bucket %v %v", *hist.Drop[119], *hist.LatMs[119])
	}
}

func TestDownsampleEdges(t *testing.T) {
	h := Downsample(nil, nil, now, HistoryBuckets)
	if h.Drop == nil || len(h.Drop) != 0 || h.LatMs == nil {
		t.Fatalf("empty input should give empty non-nil slices: %+v", h)
	}
	// NaN samples (protobuf "NaN") leave a hole, not a zero.
	h = Downsample([]float64{math.NaN(), 0}, []float64{math.NaN(), 50}, now, 2)
	if h.Drop[0] != nil || h.LatMs[0] != nil || *h.Drop[1] != 0 || *h.LatMs[1] != 50 {
		t.Fatalf("NaN handling: %v %v", h.Drop, h.LatMs)
	}
	// Uneven split: 7 samples into 3 buckets -> per=3, newest bucket full,
	// oldest bucket holds the single leftover sample.
	h = Downsample([]float64{1, 0, 0, 0, 0, 0, 0}, []float64{0, 1, 2, 3, 4, 5, 6}, now, 3)
	if len(h.Drop) != 3 || *h.Drop[0] != 1 || *h.LatMs[2] != 6 {
		t.Fatalf("uneven split: %v %v", *h.Drop[0], *h.LatMs[2])
	}
}

func TestSourceHistoryEmptyBeforePoll(t *testing.T) {
	src := New(&fakeCaller{}, Options{Now: func() time.Time { return now }, Logger: quiet()})
	h := src.History15m()
	if h.Drop == nil || len(h.Drop) != 0 {
		t.Fatalf("expected empty history, got %+v", h)
	}
}
