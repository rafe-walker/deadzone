package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestDefaultValidates(t *testing.T) {
	c := Default()
	c.Forecast.Latitude = 31.9
	c.Forecast.Longitude = -109.8
	if err := c.Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestDefaultRequiresLatLonWhenForecastEnabled(t *testing.T) {
	c := Default()
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "latitude/longitude") {
		t.Fatalf("expected lat/lon error, got %v", err)
	}
	c.Forecast.Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatalf("forecast disabled should validate: %v", err)
	}
}

func TestLoadYAMLAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "deadzone.yaml")
	yamlText := `
mqtt:
  host: 192.0.2.10
  user: sa
battery:
  capacity_kwh: 15.36
  reserve_soc_pct: 25
forecast:
  latitude: 31.5
  longitude: -110.1
shed:
  - name: water_heater
    watts: 1200
    priority: 1
`
	if err := os.WriteFile(p, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEADZONE_MQTT_HOST", "198.51.100.7")
	t.Setenv("DEADZONE_BATTERY_KWH", "20.48")
	t.Setenv("DEADZONE_HTTP_LISTEN", ":9999")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MQTT.Host != "198.51.100.7" {
		t.Errorf("env override lost: host=%q", c.MQTT.Host)
	}
	if c.MQTT.User != "sa" {
		t.Errorf("yaml value lost: user=%q", c.MQTT.User)
	}
	if c.Battery.CapacityKWh != 20.48 {
		t.Errorf("env override lost: kwh=%v", c.Battery.CapacityKWh)
	}
	if c.Battery.ReserveSOCPct != 25 {
		t.Errorf("yaml reserve lost: %v", c.Battery.ReserveSOCPct)
	}
	if c.HTTP.Listen != ":9999" {
		t.Errorf("listen=%q", c.HTTP.Listen)
	}
	if len(c.Shed) != 1 || c.Shed[0].Name != "water_heater" {
		t.Errorf("shed list not parsed: %+v", c.Shed)
	}
	if c.MQTT.Port != 1883 {
		t.Errorf("default port lost: %d", c.MQTT.Port)
	}
}

func TestEnvBadNumber(t *testing.T) {
	c := Default()
	err := c.ApplyEnv(envOf(map[string]string{"DEADZONE_LAT": "north"}))
	if err == nil || !strings.Contains(err.Error(), "DEADZONE_LAT") {
		t.Fatalf("expected DEADZONE_LAT parse error, got %v", err)
	}
}

func TestEnvBoolAndInt(t *testing.T) {
	c := Default()
	err := c.ApplyEnv(envOf(map[string]string{
		"DEADZONE_HA_DISCOVERY":     "false",
		"DEADZONE_MQTT_PORT":        "1884",
		"DEADZONE_STARLINK_ENABLED": "0",
		"DEADZONE_RESERVE_SOC":      "30",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MQTT.HADiscovery || c.MQTT.Port != 1884 || c.Starlink.Enabled || c.Battery.ReserveSOCPct != 30 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]func(*Config){
		"capacity":  func(c *Config) { c.Battery.CapacityKWh = 0 },
		"reserve":   func(c *Config) { c.Battery.ReserveSOCPct = 100 },
		"port":      func(c *Config) { c.MQTT.Port = 70000 },
		"host":      func(c *Config) { c.MQTT.Host = "" },
		"shed name": func(c *Config) { c.Shed = []ShedLoad{{Watts: 10}} },
		"stale":     func(c *Config) { c.Thresholds.StaleAfterS = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			c.Forecast.Enabled = false
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestBrokerURL(t *testing.T) {
	m := MQTT{Host: "broker", Port: 1883}
	if got := m.BrokerURL(); got != "tcp://broker:1883" {
		t.Fatalf("BrokerURL=%q", got)
	}
}
