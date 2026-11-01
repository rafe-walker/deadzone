package starlink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 17, 10, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeCaller answers from fixtures keyed by the request's top-level key.
type fakeCaller struct {
	mu    sync.Mutex
	files map[string]string
	err   error
	calls int
}

func (f *fakeCaller) Call(_ context.Context, req map[string]any) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for k := range req {
		if name, ok := f.files[k]; ok {
			b, err := os.ReadFile(filepath.Join("testdata", name))
			return b, err
		}
		return nil, fmt.Errorf("no fixture for %s", k)
	}
	return nil, errors.New("empty request")
}

func (f *fakeCaller) Close() error { return nil }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestApplyStatusSnake(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyStatus(&s, fixture(t, "get_status.json"), now); err != nil {
		t.Fatal(err)
	}
	if !s.Reachable || s.State != "CONNECTED" {
		t.Fatalf("state=%q reachable=%v", s.State, s.Reachable)
	}
	if s.HardwareVersion != "rev3_proto2" || s.SoftwareVersion != "2026.09.15.mr58000" {
		t.Errorf("versions: %q %q", s.HardwareVersion, s.SoftwareVersion)
	}
	if s.UptimeS == nil || *s.UptimeS != 86420 {
		t.Errorf("uptime (string int64) not parsed: %v", s.UptimeS)
	}
	if s.LatencyNowMs == nil || *s.LatencyNowMs != 34.5 {
		t.Errorf("latency=%v", s.LatencyNowMs)
	}
	if s.DownlinkBps == nil || *s.DownlinkBps != 24130000.5 || s.UplinkBps == nil || *s.UplinkBps != 2810000.25 {
		t.Errorf("throughput: %v %v", s.DownlinkBps, s.UplinkBps)
	}
	if s.ObstructedNow == nil || *s.ObstructedNow || s.FractionObstructed == nil || *s.FractionObstructed != 0.004 {
		t.Errorf("obstruction: %v %v", s.ObstructedNow, s.FractionObstructed)
	}
	if s.ObstructionDurS == nil || *s.ObstructionDurS != 2.5 {
		t.Errorf("obstruction duration: %v", s.ObstructionDurS)
	}
	if len(s.Alerts) != 1 || s.Alerts[0] != "is_heating" {
		t.Errorf("alerts=%v want [is_heating]", s.Alerts)
	}
	if s.SNRAboveNoiseFloor == nil || !*s.SNRAboveNoiseFloor {
		t.Errorf("snr flag: %v", s.SNRAboveNoiseFloor)
	}
	if !s.Updated.Equal(now) {
		t.Errorf("updated=%v", s.Updated)
	}
}

func TestApplyStatusCamelObstructed(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyStatus(&s, fixture(t, "get_status_obstructed_camel.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.State != "SEARCHING" {
		t.Errorf("state=%q", s.State)
	}
	if s.ObstructedNow == nil || !*s.ObstructedNow {
		t.Errorf("currentlyObstructed not read from camelCase")
	}
	if s.FractionObstructed == nil || *s.FractionObstructed != 0.18 {
		t.Errorf("fraction=%v", s.FractionObstructed)
	}
	// known alert first (thermal_throttle), unknown-but-true appended (dish_water_detected is in the list too)
	if len(s.Alerts) != 2 || s.Alerts[0] != "thermal_throttle" || s.Alerts[1] != "dish_water_detected" {
		t.Errorf("alerts=%v", s.Alerts)
	}
	if s.UptimeS == nil || *s.UptimeS != 120 {
		t.Errorf("uptime=%v", s.UptimeS)
	}
}

func TestApplyStatusRejectsOtherResponse(t *testing.T) {
	s := New(nil, Options{}).state
	err := ApplyStatus(&s, []byte(`{"dish_get_history":{}}`), now)
	if err == nil {
		t.Fatal("expected error for wrong response type")
	}
	if err := ApplyStatus(&s, []byte(`not json`), now); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestApplyHistoryRingBuffer(t *testing.T) {
	s := New(nil, Options{}).state
	if err := ApplyHistory(&s, fixture(t, "get_history.json"), now); err != nil {
		t.Fatal(err)
	}
	if s.HistorySamples != 120 {
		t.Fatalf("samples=%d want 120 (ring shorter than window)", s.HistorySamples)
	}
	// 5 full drops + one 0.5 over 120 samples = 5.5/120
	if s.Drop5m == nil || math.Abs(*s.Drop5m-5.5/120) > 1e-9 {
		t.Errorf("drop_5m=%v want %v", s.Drop5m, 5.5/120)
	}
	if s.LatP50Ms == nil || *s.LatP50Ms != 39.32 {
		t.Errorf("p50=%v want 39.32", s.LatP50Ms)
	}
	if s.LatP90Ms == nil || *s.LatP90Ms != 48.2 {
		t.Errorf("p90=%v want 48.2", s.LatP90Ms)
	}
	if s.JitterMs == nil || *s.JitterMs <= 0 || *s.JitterMs > 30 {
		t.Errorf("jitter=%v", s.JitterMs)
	}
	// newest power sample is logical index 119 -> 60.9
	if s.PowerW == nil || *s.PowerW != 60.9 {
		t.Errorf("power_w=%v want 60.9 (newest ring sample)", s.PowerW)
	}
	if len(s.Outages15m) != 1 {
		t.Fatalf("outages=%d want 1 (the 16:26 one is older than 15 min)", len(s.Outages15m))
	}
	o := s.Outages15m[0]
	if o.Cause != "NO_SCHEDULE" || o.DurationS != 5 || !o.DidSwitch {
		t.Errorf("outage=%+v", o)
	}
	if !o.Start.Equal(time.Date(2026, 10, 31, 17, 5, 0, 0, time.UTC)) {
		t.Errorf("outage start=%v", o.Start)
	}
}

func TestOrderedRing(t *testing.T) {
	ring := []float64{3, 4, 0, 1, 2} // current=8 -> start=3 -> [1,2,3,4,0]? no: ring[3:]=1,2 then ring[:3]=3,4,0
	got := ordered(ring, 8)
	want := []float64{1, 2, 3, 4, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordered=%v want %v", got, want)
		}
	}
	// not yet wrapped: only the first `current` samples are valid
	got = ordered([]float64{9, 8, 7, 0, 0}, 3)
	if len(got) != 3 || got[2] != 7 {
		t.Fatalf("partial ring=%v", got)
	}
	if ordered(nil, 5) != nil {
		t.Fatal("empty ring")
	}
}

func TestPercentileNearestRank(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := percentile(s, 0.5); p != 5 {
		t.Errorf("p50=%v", p)
	}
	if p := percentile(s, 0.9); p != 9 {
		t.Errorf("p90=%v", p)
	}
	if p := percentile(nil, 0.9); p != 0 {
		t.Errorf("empty=%v", p)
	}
}

func TestRelativeOutageTimestampsKept(t *testing.T) {
	s := New(nil, Options{}).state
	raw := []byte(`{"dish_get_history":{"current":"3","pop_ping_drop_rate":[0,0,0],"pop_ping_latency_ms":[30,31,32],
	  "outages":[{"cause":"NO_SATS","start_timestamp_ns":"123456789","duration_ns":"7000000000"}]}}`)
	if err := ApplyHistory(&s, raw, now); err != nil {
		t.Fatal(err)
	}
	if len(s.Outages15m) != 1 || !s.Outages15m[0].Start.IsZero() || s.Outages15m[0].DurationS != 7 {
		t.Fatalf("relative-timestamp outage should be kept with zero Start: %+v", s.Outages15m)
	}
}

func TestSourcePollsAndSnapshots(t *testing.T) {
	fc := &fakeCaller{files: map[string]string{"get_status": "get_status.json", "get_history": "get_history.json"}}
	clock := now
	src := New(fc, Options{Now: func() time.Time { return clock }, Logger: quiet()})
	ctx := context.Background()
	src.PollStatus(ctx)
	src.PollHistory(ctx)
	st := src.Starlink()
	if !st.Reachable || st.State != "CONNECTED" || st.Drop5m == nil || st.LatP90Ms == nil {
		t.Fatalf("merged snapshot incomplete: %+v", st)
	}
	snap := src.Snapshot()
	if snap.Starlink == nil || snap.Power != nil || snap.Forecast != nil {
		t.Fatal("snapshot should carry only Starlink")
	}
	if src.Name() != "starlink" {
		t.Fatal("name")
	}
	// Staleness: 31 s without a poll -> unreachable but numbers retained.
	clock = clock.Add(31 * time.Second)
	st = src.Starlink()
	if st.Reachable {
		t.Fatal("stale telemetry must not read as reachable")
	}
	if st.LatP90Ms == nil {
		t.Fatal("last good numbers should still be present for display")
	}
}

func TestReflectionFailureIsUnknownNotDown(t *testing.T) {
	fc := &fakeCaller{err: fmt.Errorf("%w: proto: not found", ErrReflection)}
	src := New(fc, Options{Now: func() time.Time { return now }, Logger: quiet()})
	src.PollStatus(context.Background())
	st := src.Starlink()
	if st.Error == "" || !errors.Is(fc.err, ErrReflection) {
		t.Fatalf("error should be recorded: %+v", st)
	}
	if st.Reachable {
		t.Fatal("never polled successfully -> not reachable")
	}
	if !st.Present {
		t.Fatal("configured dish stays Present so the UI shows 'unknown', not 'no dish'")
	}
}

func TestUnreachableClearsReachableKeepsLastGood(t *testing.T) {
	fc := &fakeCaller{files: map[string]string{"get_status": "get_status.json"}}
	clock := now
	src := New(fc, Options{Now: func() time.Time { return clock }, Logger: quiet()})
	src.PollStatus(context.Background())
	if !src.Starlink().Reachable {
		t.Fatal("first poll should succeed")
	}
	fc.mu.Lock()
	fc.err = fmt.Errorf("%w: connection refused", ErrUnreachable)
	fc.mu.Unlock()
	clock = clock.Add(5 * time.Second)
	src.PollStatus(context.Background())
	st := src.Starlink()
	if st.Reachable {
		t.Fatal("transport failure must clear Reachable")
	}
	if st.State != "CONNECTED" {
		t.Fatal("last good state should remain for display")
	}
}

func TestStartRunsFirstPollInline(t *testing.T) {
	fc := &fakeCaller{files: map[string]string{"get_status": "get_status.json", "get_history": "get_history.json"}}
	src := New(fc, Options{Now: func() time.Time { return now }, Logger: quiet(), StatusInterval: time.Hour, HistoryInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := src.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if src.Starlink().State != "CONNECTED" {
		t.Fatal("Start should poll status inline")
	}
}

func TestReflectCallerUnreachableAddress(t *testing.T) {
	// 127.0.0.1:1 refuses immediately; this exercises the real client's
	// error classification without any network access.
	rc := NewReflectCaller("127.0.0.1:1", 2*time.Second)
	defer rc.Close()
	_, err := rc.Call(context.Background(), map[string]any{"get_status": map[string]any{}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnreachable) && !errors.Is(err, ErrReflection) {
		t.Fatalf("error should be classified, got %v", err)
	}
}

func TestCamelSnakeHelpers(t *testing.T) {
	if camel("pop_ping_drop_rate") != "popPingDropRate" {
		t.Fatal(camel("pop_ping_drop_rate"))
	}
	if snake("popPingDropRate") != "pop_ping_drop_rate" {
		t.Fatal(snake("popPingDropRate"))
	}
}
