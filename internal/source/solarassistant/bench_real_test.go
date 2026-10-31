package solarassistant

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

// TestRealBenchCapture replays a 45 s capture of the real Solar Assistant
// broker on the author's bench (two EG4 3000EHV-48 on the "Sumry Off-Grid"
// driver, SOC from inverter voltage, taken 2026-10-31 15:33 MST). It pins
// the topic names and value shapes SA actually publishes, so a future SA
// change that renames a topic breaks this test rather than the verdict.
func TestRealBenchCapture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "bench_real_2026-10-31.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	c := &clock{}
	s := newSource(t, c)
	var n int
	var first, last float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m struct {
			T       float64 `json:"t"`
			Topic   string  `json:"topic"`
			Payload string  `json:"payload"`
		}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if first == 0 {
			first = m.T
		}
		last = m.T
		c.t = time.Unix(0, int64(m.T*1e9))
		s.Handle(m.Topic, []byte(m.Payload))
		n++
	}
	if n < 1000 {
		t.Fatalf("expected a full capture, got %d messages", n)
	}
	if last-first < 40 {
		t.Fatalf("capture should span ~45 s, spans %.1f s", last-first)
	}
	// Only solar_assistant/* messages count; homeassistant/* discovery
	// configs are ignored by the source.
	if s.MessageCount() == 0 {
		t.Fatal("no solar_assistant messages were counted")
	}

	p := s.Power()
	if p.Stale {
		t.Fatalf("live capture should not be stale: %v", p.Caveats)
	}
	if len(p.Inverters) != 2 {
		t.Fatalf("inverters=%d want 2 (bench has two parallel units)", len(p.Inverters))
	}
	if p.Inverters[0].ID != "inverter_1" || p.Inverters[1].ID != "inverter_2" {
		t.Fatalf("inverter ids: %s %s", p.Inverters[0].ID, p.Inverters[1].ID)
	}
	for _, inv := range p.Inverters {
		if inv.Serial == "" || inv.Mode == "" || inv.BattV == nil || inv.PVW == nil || inv.TempC == nil {
			t.Errorf("%s missing core fields: %+v", inv.ID, inv)
		}
		if inv.ShutdownV == nil || *inv.ShutdownV != 47.5 {
			t.Errorf("%s shutdown_battery_voltage=%v want 47.5", inv.ID, inv.ShutdownV)
		}
	}
	// SOC on this bench comes from inverter voltage (no battery_N device).
	if p.SOCPct == nil || *p.SOCPct <= 0 || *p.SOCPct > 100 {
		t.Fatalf("soc=%v want 0<soc<=100", p.SOCPct)
	}
	if p.SOCSource != model.SOCInverterVoltage {
		t.Errorf("soc source=%s want inverter_voltage", p.SOCSource)
	}
	if len(p.Packs) != 0 {
		t.Errorf("no BMS packs expected on this bench, got %d", len(p.Packs))
	}
	// total/* must be preferred over summing inverters: SA reports
	// inverter_2 load_power as -1 (slave unit), so a naive sum is wrong.
	if p.LoadW == nil || *p.LoadW < 100 || *p.LoadW > 2000 {
		t.Errorf("load_w=%v outside the plausible bench range", p.LoadW)
	}
	if p.PVW == nil || *p.PVW < 0 {
		t.Errorf("pv_w=%v", p.PVW)
	}
	if p.BattV == nil || *p.BattV < 44 || *p.BattV > 58 {
		t.Errorf("batt_v=%v outside 48 V LFP range", p.BattV)
	}
	t.Logf("bench: soc=%.0f%% load=%.0fW pv=%.0fW batt=%.1fV inv1=%s inv2=%s",
		*p.SOCPct, *p.LoadW, *p.PVW, *p.BattV, p.Inverters[0].Mode, p.Inverters[1].Mode)
}
