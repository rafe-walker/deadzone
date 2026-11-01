package bus

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/model"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sampleState() model.State {
	until := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	st := model.State{TS: time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC), Version: "test"}
	st.Power.SOCPct = model.F(71)
	st.Power.SOCTrust = model.TrustLow
	st.Power.PVW = model.F(1180)
	st.Power.LoadW = model.F(690)
	st.Verdict.CallOK = model.Green
	st.Verdict.HoursWorkstation = 7.3
	st.Verdict.HoursWithPV = 11.8
	st.Verdict.UntilReserve = &until
	st.Verdict.CallReasons = []string{"uplink:drop_5m=0.01<=0.02"}
	st.Verdict.Shed = []model.ShedItem{{Load: "water_heater", W: 1200, GainH: 3.1}}
	st.Verdict.UplinkScore = 91
	st.Forecast.PVWhNext6h = 5200
	st.Forecast.PRLearned = 0.78
	return st
}

func TestStateMessages(t *testing.T) {
	msgs, err := StateMessages("deadzone", sampleState())
	if err != nil {
		t.Fatal(err)
	}
	byTopic := map[string]Message{}
	for _, m := range msgs {
		if !m.Retain {
			t.Errorf("%s should be retained", m.Topic)
		}
		if !strings.HasPrefix(m.Topic, "deadzone/") {
			t.Errorf("topic outside prefix: %s", m.Topic)
		}
		byTopic[m.Topic] = m
	}
	for _, want := range []string{"deadzone/state", "deadzone/verdict", "deadzone/hours_at_soc", "deadzone/shed_list", "deadzone/verdict/call_reasons", "deadzone/verdict/until", "deadzone/power/soc_trust", "deadzone/uplink/starlink/score", "deadzone/forecast/pv_wh_next_6h"} {
		if _, ok := byTopic[want]; !ok {
			t.Errorf("missing topic %s", want)
		}
	}
	if string(byTopic["deadzone/verdict"].Payload) != "green" {
		t.Errorf("verdict payload=%q", byTopic["deadzone/verdict"].Payload)
	}
	if string(byTopic["deadzone/hours_at_soc"].Payload) != "7.30" {
		t.Errorf("hours payload=%q", byTopic["deadzone/hours_at_soc"].Payload)
	}
	if string(byTopic["deadzone/verdict/until"].Payload) != "2026-10-31T23:30:00Z" {
		t.Errorf("until payload=%q", byTopic["deadzone/verdict/until"].Payload)
	}
	if string(byTopic["deadzone/uplink/starlink/score"].Payload) != "91" {
		t.Errorf("score payload=%q", byTopic["deadzone/uplink/starlink/score"].Payload)
	}
	var shed []model.ShedItem
	if err := json.Unmarshal(byTopic["deadzone/shed_list"].Payload, &shed); err != nil || len(shed) != 1 || shed[0].Load != "water_heater" {
		t.Errorf("shed payload: %s (%v)", byTopic["deadzone/shed_list"].Payload, err)
	}
	var full model.State
	if err := json.Unmarshal(byTopic["deadzone/state"].Payload, &full); err != nil {
		t.Fatalf("state JSON round trip: %v", err)
	}
	if full.Verdict.CallOK != model.Green || *full.Power.SOCPct != 71 {
		t.Errorf("state JSON lost fields: %+v", full)
	}
}

func TestStateMessagesNoUntil(t *testing.T) {
	st := sampleState()
	st.Verdict.UntilReserve = nil
	msgs, err := StateMessages("dz", st)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Topic == "dz/verdict/until" && len(m.Payload) != 0 {
			t.Errorf("until should be empty when unknown, got %q", m.Payload)
		}
	}
}

func TestDiscoveryMessages(t *testing.T) {
	msgs, err := DiscoveryMessages("homeassistant", "deadzone", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != len(Entities) {
		t.Fatalf("got %d messages for %d entities", len(msgs), len(Entities))
	}
	required := map[string]string{
		"verdict": "sensor", "call_ok": "binary_sensor", "hours_at_current_load": "sensor",
		"battery_soc": "sensor", "pv_w": "sensor", "load_w": "sensor",
		"starlink_pop_ping_ms": "sensor", "starlink_downlink_mbps": "sensor",
		"starlink_obstructed": "binary_sensor", "forecast_cloud_pct": "sensor",
	}
	seenTopics := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, m := range msgs {
		if !m.Retain {
			t.Errorf("discovery must be retained: %s", m.Topic)
		}
		if seenTopics[m.Topic] {
			t.Errorf("duplicate topic %s", m.Topic)
		}
		seenTopics[m.Topic] = true
		var cfg map[string]any
		if err := json.Unmarshal(m.Payload, &cfg); err != nil {
			t.Fatalf("%s: %v", m.Topic, err)
		}
		uid, _ := cfg["unique_id"].(string)
		if seenIDs[uid] {
			t.Errorf("duplicate unique_id %s", uid)
		}
		seenIDs[uid] = true
		if cfg["state_topic"] != "deadzone/state" || cfg["availability_topic"] != "deadzone/availability" {
			t.Errorf("%s: topics %v %v", m.Topic, cfg["state_topic"], cfg["availability_topic"])
		}
		if _, ok := cfg["value_template"].(string); !ok {
			t.Errorf("%s: no value_template", m.Topic)
		}
		dev, _ := cfg["device"].(map[string]any)
		if dev == nil || dev["sw_version"] != "0.1.0" {
			t.Errorf("%s: device block %v", m.Topic, dev)
		}
		if strings.Contains(m.Topic, "/binary_sensor/") {
			if cfg["payload_on"] != "True" || cfg["payload_off"] != "False" {
				t.Errorf("%s: binary payloads %v/%v", m.Topic, cfg["payload_on"], cfg["payload_off"])
			}
		}
	}
	for key, comp := range required {
		topic := "homeassistant/" + comp + "/deadzone/" + key + "/config"
		if !seenTopics[topic] {
			t.Errorf("missing required entity %s", topic)
		}
	}
}

func TestDiscoveryTopicShape(t *testing.T) {
	got := DiscoveryTopic("ha", Entity{Component: "sensor", Key: "verdict"})
	if got != "ha/sensor/deadzone/verdict/config" {
		t.Fatal(got)
	}
}

func TestClientWithoutBroker(t *testing.T) {
	cfg := config.Default().MQTT
	cfg.Host = "127.0.0.1"
	cfg.Port = 1 // nothing listens
	c := New(cfg, "test", quiet())
	if c.Topics().Availability() != "deadzone/availability" {
		t.Fatal(c.Topics().Availability())
	}
	called := false
	if err := c.Subscribe("solar_assistant/#", func(string, []byte) { called = true }); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe("", nil); err == nil {
		t.Fatal("empty subscription should fail")
	}
	if err := c.Publish("deadzone/verdict", []byte("green"), true); err == nil {
		t.Fatal("publish before connect must fail")
	}
	if err := c.PublishState(sampleState()); err == nil {
		t.Fatal("publish state before connect must fail")
	}
	if c.Connected() {
		t.Fatal("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("connect to a closed port must report an error")
	}
	if called {
		t.Fatal("handler should not fire")
	}
	c.Close()
	c.Close() // idempotent
}

func TestCloseNeverConnected(t *testing.T) {
	c := New(config.Default().MQTT, "test", nil)
	c.Close()
}

func TestDiscoveryStarlinkPowerAndDisablement(t *testing.T) {
	msgs, err := DiscoveryMessages("homeassistant", "deadzone", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	byTopic := map[string]map[string]any{}
	for _, m := range msgs {
		var cfg map[string]any
		if err := json.Unmarshal(m.Payload, &cfg); err != nil {
			t.Fatal(err)
		}
		byTopic[m.Topic] = cfg
	}
	pw := byTopic["homeassistant/sensor/deadzone/starlink_power_w/config"]
	if pw == nil {
		t.Fatal("starlink_power_w entity missing")
	}
	if pw["device_class"] != "power" || pw["unit_of_measurement"] != "W" || pw["state_class"] != "measurement" {
		t.Errorf("starlink_power_w config: %v", pw)
	}
	if tpl, _ := pw["value_template"].(string); !strings.Contains(tpl, "power_mean_5m_w") {
		t.Errorf("power template should prefer the 5-min mean: %q", tpl)
	}
	dc := byTopic["homeassistant/sensor/deadzone/starlink_disablement_code/config"]
	if dc == nil || dc["entity_category"] != "diagnostic" {
		t.Fatalf("starlink_disablement_code should be a diagnostic entity: %v", dc)
	}
}

func TestDiscoveryVerdictAttributes(t *testing.T) {
	msgs, err := DiscoveryMessages("homeassistant", "deadzone", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		var cfg map[string]any
		_ = json.Unmarshal(m.Payload, &cfg)
		if m.Topic == "homeassistant/sensor/deadzone/verdict/config" {
			if cfg["json_attributes_topic"] != "deadzone/state" {
				t.Fatalf("verdict needs attributes from the state topic: %v", cfg)
			}
			tpl, _ := cfg["json_attributes_template"].(string)
			if !strings.Contains(tpl, "call_reasons") || !strings.Contains(tpl, "tojson") {
				t.Fatalf("attributes template: %q", tpl)
			}
			return
		}
	}
	t.Fatal("verdict entity missing")
}
