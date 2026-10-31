package solarassistant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

type fixture struct {
	Note     string `json:"_note"`
	Messages []struct {
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
	} `json:"messages"`
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Messages) == 0 {
		t.Fatalf("fixture %s has no messages", name)
	}
	return f
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSource(t *testing.T, c *clock) *Source {
	t.Helper()
	return New(nil, Options{
		CapacityWh:                10240,
		ReserveSOCPct:             20,
		BatteryPositiveIsCharging: true,
		Now:                       c.now,
	})
}

func feed(s *Source, f fixture) {
	for _, m := range f.Messages {
		s.Handle(m.Topic, []byte(m.Payload))
	}
}

func f64(p *float64) float64 {
	if p == nil {
		return -99999
	}
	return *p
}

func TestTwoInverterBenchTotals(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)}
	s := newSource(t, c)
	feed(s, loadFixture(t, "bench_two_inverters.json"))
	p := s.Power()

	if p.Stale {
		t.Fatalf("fresh data should not be stale: %+v", p.Caveats)
	}
	if got := f64(p.LoadW); got != 690 {
		t.Errorf("load_w=%v want 690", got)
	}
	if got := f64(p.PVW); got != 1180 {
		t.Errorf("pv_w=%v want 1180", got)
	}
	if got := f64(p.SOCPct); got != 71 {
		t.Errorf("soc=%v want 71", got)
	}
	if p.SOCSource != model.SOCInverterVoltage || p.SOCTrust != model.TrustLow {
		t.Errorf("soc source/trust = %s/%s, want inverter_voltage/low", p.SOCSource, p.SOCTrust)
	}
	if got := f64(p.BattW); got != 412 {
		t.Errorf("batt_w=%v want 412 (positive=charging)", got)
	}
	if got := f64(p.BattV); got != 52.6 {
		t.Errorf("batt_v=%v want 52.6 (mean of inverters)", got)
	}
	if len(p.Inverters) != 2 {
		t.Fatalf("inverters=%d want 2", len(p.Inverters))
	}
	if p.Inverters[0].ID != "inverter_1" || p.Inverters[1].ID != "inverter_2" {
		t.Errorf("inverter order: %s %s", p.Inverters[0].ID, p.Inverters[1].ID)
	}
	if p.Inverters[0].Mode != "Battery" || f64(p.Inverters[1].LoadW) != 340 {
		t.Errorf("inverter fields: %+v", p.Inverters)
	}
	if p.Inverters[0].OutputP != "Solar first" || f64(p.Inverters[0].ShutdownV) != 47.5 {
		t.Errorf("inverter settings: %+v", p.Inverters[0])
	}
	if len(p.Packs) != 0 {
		t.Errorf("no battery_N group expected, got %d packs", len(p.Packs))
	}
	// usable = 10240 * (71-20)/100 = 5222.4
	if p.UsableWh < 5222 || p.UsableWh > 5223 {
		t.Errorf("usable_wh=%v want ~5222.4", p.UsableWh)
	}
	if got := f64(p.LoadEMAW); got != 690 {
		t.Errorf("first EMA sample should equal load: %v", got)
	}
}

func TestBMSPackWinsSOC(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC)}
	s := newSource(t, c)
	feed(s, loadFixture(t, "bms_pack.json"))
	p := s.Power()
	if p.SOCSource != model.SOCBMS || p.SOCTrust != model.TrustHigh {
		t.Fatalf("soc source/trust = %s/%s, want bms/high", p.SOCSource, p.SOCTrust)
	}
	if got := f64(p.SOCPct); got != 61 {
		t.Errorf("soc=%v want 61 (BMS), not the inverter's 55", got)
	}
	if len(p.Packs) != 1 || f64(p.Packs[0].CellMinV) != 3.241 {
		t.Errorf("pack parse: %+v", p.Packs)
	}
	if got := f64(p.BattW); got != -820 {
		t.Errorf("batt_w=%v want -820 (discharging)", got)
	}
}

func TestBatterySignFlip(t *testing.T) {
	c := &clock{t: time.Now()}
	s := New(nil, Options{BatteryPositiveIsCharging: false, Now: c.now})
	s.Handle("solar_assistant/total/battery_power/state", []byte("300"))
	if got := f64(s.Power().BattW); got != -300 {
		t.Fatalf("expected sign flip, got %v", got)
	}
}

func TestStaleAfterThirtySeconds(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)}
	s := newSource(t, c)
	feed(s, loadFixture(t, "bench_two_inverters.json"))
	c.t = c.t.Add(29 * time.Second)
	if s.Power().Stale {
		t.Fatal("29 s should not be stale")
	}
	c.t = c.t.Add(2 * time.Second)
	p := s.Power()
	if !p.Stale {
		t.Fatal("31 s should be stale")
	}
	if len(p.Caveats) == 0 {
		t.Error("stale snapshot should carry a caveat")
	}
}

func TestNoMessagesIsStale(t *testing.T) {
	s := newSource(t, &clock{t: time.Now()})
	p := s.Power()
	if !p.Stale || p.SOCPct != nil || p.SOCSource != model.SOCUnknown {
		t.Fatalf("empty source should be stale/unknown: %+v", p)
	}
}

func TestFallbackToInverterSums(t *testing.T) {
	c := &clock{t: time.Now()}
	s := newSource(t, c)
	s.Handle("solar_assistant/inverter_1/load_power/state", []byte("300"))
	s.Handle("solar_assistant/inverter_2/load_power/state", []byte("250"))
	s.Handle("solar_assistant/inverter_1/pv_power/state", []byte("100"))
	s.Handle("solar_assistant/inverter_1/battery_state_of_charge/state", []byte("60"))
	s.Handle("solar_assistant/inverter_2/battery_state_of_charge/state", []byte("64"))
	p := s.Power()
	if f64(p.LoadW) != 550 {
		t.Errorf("load sum=%v want 550", f64(p.LoadW))
	}
	if f64(p.PVW) != 100 {
		t.Errorf("pv sum=%v want 100", f64(p.PVW))
	}
	if f64(p.SOCPct) != 62 {
		t.Errorf("soc mean=%v want 62", f64(p.SOCPct))
	}
}

func TestIgnoresForeignAndMalformedTopics(t *testing.T) {
	s := newSource(t, &clock{t: time.Now()})
	s.Handle("homeassistant/sensor/x/config", []byte("{}"))
	s.Handle("solar_assistant/inverter_1/load_power/state/extra", []byte("1"))
	s.Handle("solar_assistant/inverter_1/load_power/state", []byte("not-a-number"))
	if s.MessageCount() != 1 {
		t.Fatalf("expected 1 accepted message, got %d", s.MessageCount())
	}
	if s.Power().LoadW != nil {
		t.Fatal("non-numeric payload must not produce a number")
	}
}

func TestLoadEMADecays(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	s := New(nil, Options{Now: c.now, LoadEMATau: 10 * time.Minute})
	s.Handle("solar_assistant/total/load_power/state", []byte("200"))
	c.t = c.t.Add(10 * time.Minute)
	s.Handle("solar_assistant/total/load_power/state", []byte("1200"))
	ema := f64(s.Power().LoadEMAW)
	// after one tau, alpha = 1-e^-1 = 0.632 -> 200 + 0.632*1000 = 832
	if ema < 830 || ema > 834 {
		t.Fatalf("ema=%v want ~832", ema)
	}
}

func TestUsableNeverNegative(t *testing.T) {
	s := newSource(t, &clock{t: time.Now()})
	s.Handle("solar_assistant/total/battery_state_of_charge/state", []byte("15"))
	if u := s.Power().UsableWh; u != 0 {
		t.Fatalf("usable below reserve should clamp to 0, got %v", u)
	}
}

type fakeSub struct{ topics []string }

func (f *fakeSub) Subscribe(topic string, h func(string, []byte)) error {
	f.topics = append(f.topics, topic)
	h("solar_assistant/total/load_power/state", []byte("5"))
	return nil
}

func TestStartSubscribesWildcard(t *testing.T) {
	fs := &fakeSub{}
	s := New(fs, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fs.topics) != 1 || fs.topics[0] != "solar_assistant/#" {
		t.Fatalf("subscribed to %v", fs.topics)
	}
	if f64(s.Power().LoadW) != 5 {
		t.Fatal("message via subscription not handled")
	}
	if s.Name() != "solarassistant" {
		t.Fatal("name")
	}
	if got := s.Devices(); len(got) != 1 || got[0] != "total" {
		t.Fatalf("devices=%v", got)
	}
}
