// Package config loads the deadzone configuration from a YAML file, applies
// defaults, then applies DEADZONE_* environment overrides. Every key is
// documented in config.example.yaml at the repo root.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MQTT holds broker settings and the topic namespaces.
type MQTT struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	User        string `yaml:"user"`
	Pass        string `yaml:"pass"`
	ClientID    string `yaml:"client_id"`
	SAPrefix    string `yaml:"sa_prefix"`
	TopicPrefix string `yaml:"topic_prefix"`
	HAPrefix    string `yaml:"ha_prefix"`
	HADiscovery bool   `yaml:"ha_discovery"`
	// BatteryPositiveIsCharging flips the sign convention of
	// total/battery_power. Solar Assistant's sign varies by driver.
	BatteryPositiveIsCharging bool `yaml:"battery_positive_is_charging"`
}

// Starlink holds the local gRPC poller settings.
type Starlink struct {
	Enabled          bool    `yaml:"enabled"`
	Addr             string  `yaml:"addr"`
	StatusIntervalS  float64 `yaml:"status_interval_s"`
	HistoryIntervalS float64 `yaml:"history_interval_s"`
	TimeoutS         float64 `yaml:"timeout_s"`
	DishWatts        float64 `yaml:"dish_watts"`
}

// Forecast holds the Open-Meteo settings.
type Forecast struct {
	Enabled        bool    `yaml:"enabled"`
	Latitude       float64 `yaml:"latitude"`
	Longitude      float64 `yaml:"longitude"`
	TiltDeg        float64 `yaml:"tilt_deg"`
	AzimuthDeg     float64 `yaml:"azimuth_deg"`
	ArrayKWp       float64 `yaml:"array_kwp"`
	PRDefault      float64 `yaml:"pr_default"`
	RefreshMinutes int     `yaml:"refresh_minutes"`
	CachePath      string  `yaml:"cache_path"`
	BaseURL        string  `yaml:"base_url"`
}

// Battery holds the pack model used by Q2.
type Battery struct {
	CapacityKWh   float64 `yaml:"capacity_kwh"`
	ReserveSOCPct float64 `yaml:"reserve_soc_pct"`
	ParasiticW    float64 `yaml:"parasitic_w"`
}

// Workstation describes the load you are protecting.
type Workstation struct {
	Watts         float64 `yaml:"watts"`
	CallLengthMin float64 `yaml:"call_length_min"`
	TargetHours   float64 `yaml:"target_hours"`
}

// UplinkThresholds are the Q1 traffic-light cut-offs.
type UplinkThresholds struct {
	GreenDrop     float64 `yaml:"green_drop"`
	GreenP90Ms    float64 `yaml:"green_p90_ms"`
	GreenJitterMs float64 `yaml:"green_jitter_ms"`
	AmberDrop     float64 `yaml:"amber_drop"`
	AmberP90Ms    float64 `yaml:"amber_p90_ms"`
	AmberJitterMs float64 `yaml:"amber_jitter_ms"`
	OutageS       float64 `yaml:"outage_s"`
	StormPrecipMM float64 `yaml:"storm_precip_mm"`
}

// Thresholds groups every tunable the engine reads.
type Thresholds struct {
	StaleAfterS    float64          `yaml:"stale_after_s"`
	CallMarginPct  float64          `yaml:"call_margin_pct"`
	RedSOCAboveRes float64          `yaml:"red_soc_above_reserve_pct"`
	Uplink         UplinkThresholds `yaml:"uplink"`
}

// ShedLoad is one load the operator could turn off before a call.
type ShedLoad struct {
	Name     string  `yaml:"name"`
	Watts    float64 `yaml:"watts"`
	Control  string  `yaml:"control"`
	Priority int     `yaml:"priority"`
}

// HTTP holds the status server settings.
type HTTP struct {
	Listen string `yaml:"listen"`
}

// Config is the whole file.
type Config struct {
	LogLevel    string      `yaml:"log_level"`
	MQTT        MQTT        `yaml:"mqtt"`
	Starlink    Starlink    `yaml:"starlink"`
	Forecast    Forecast    `yaml:"forecast"`
	Battery     Battery     `yaml:"battery"`
	Workstation Workstation `yaml:"workstation"`
	Thresholds  Thresholds  `yaml:"thresholds"`
	Shed        []ShedLoad  `yaml:"shed"`
	HTTP        HTTP        `yaml:"http"`
}

// Default returns the built-in defaults. Numbers mirror §6.4 of the scoping
// doc and config.example.yaml.
func Default() Config {
	return Config{
		LogLevel: "info",
		MQTT: MQTT{
			Host:                      "solar-assistant.local",
			Port:                      1883,
			ClientID:                  "deadzone",
			SAPrefix:                  "solar_assistant",
			TopicPrefix:               "deadzone",
			HAPrefix:                  "homeassistant",
			HADiscovery:               true,
			BatteryPositiveIsCharging: true,
		},
		Starlink: Starlink{
			Enabled:          true,
			Addr:             "192.168.100.1:9200",
			StatusIntervalS:  5,
			HistoryIntervalS: 60,
			TimeoutS:         4,
			DishWatts:        60,
		},
		Forecast: Forecast{
			Enabled:        true,
			TiltDeg:        30,
			AzimuthDeg:     0,
			ArrayKWp:       0,
			PRDefault:      0.78,
			RefreshMinutes: 60,
			CachePath:      "/var/lib/deadzone/forecast.json",
			BaseURL:        "https://api.open-meteo.com/v1/forecast",
		},
		Battery: Battery{
			CapacityKWh:   10.24,
			ReserveSOCPct: 20,
			ParasiticW:    60,
		},
		Workstation: Workstation{
			Watts:         200,
			CallLengthMin: 60,
			TargetHours:   8,
		},
		Thresholds: Thresholds{
			StaleAfterS:    30,
			CallMarginPct:  25,
			RedSOCAboveRes: 5,
			Uplink: UplinkThresholds{
				GreenDrop:     0.02,
				GreenP90Ms:    150,
				GreenJitterMs: 30,
				AmberDrop:     0.05,
				AmberP90Ms:    250,
				AmberJitterMs: 50,
				OutageS:       5,
				StormPrecipMM: 2,
			},
		},
		HTTP: HTTP{Listen: ":8099"},
	}
}

// Load reads the YAML file at path (optional: "" means defaults only),
// then applies environment overrides, then validates.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
	}
	if err := cfg.ApplyEnv(os.LookupEnv); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

// LookupFunc matches os.LookupEnv so tests can inject an environment.
type LookupFunc func(string) (string, bool)

// EnvKeys lists every supported override, in documentation order.
var EnvKeys = []string{
	"DEADZONE_LOG_LEVEL",
	"DEADZONE_MQTT_HOST", "DEADZONE_MQTT_PORT", "DEADZONE_MQTT_USER", "DEADZONE_MQTT_PASS",
	"DEADZONE_MQTT_CLIENT_ID", "DEADZONE_SA_PREFIX", "DEADZONE_TOPIC_PREFIX", "DEADZONE_HA_PREFIX",
	"DEADZONE_HA_DISCOVERY",
	"DEADZONE_STARLINK_ENABLED", "DEADZONE_STARLINK_ADDR", "DEADZONE_STARLINK_DISH_WATTS",
	"DEADZONE_FORECAST_ENABLED", "DEADZONE_LAT", "DEADZONE_LON", "DEADZONE_TILT_DEG",
	"DEADZONE_AZIMUTH_DEG", "DEADZONE_ARRAY_KWP", "DEADZONE_FORECAST_CACHE",
	"DEADZONE_BATTERY_KWH", "DEADZONE_RESERVE_SOC", "DEADZONE_PARASITIC_W",
	"DEADZONE_WORKSTATION_W", "DEADZONE_CALL_LENGTH_MIN", "DEADZONE_TARGET_HOURS",
	"DEADZONE_STALE_AFTER_S", "DEADZONE_HTTP_LISTEN",
}

// ApplyEnv overrides fields from DEADZONE_* variables.
func (c *Config) ApplyEnv(lookup LookupFunc) error {
	str := func(key string, dst *string) error {
		if v, ok := lookup(key); ok {
			*dst = v
		}
		return nil
	}
	num := func(key string, dst *float64) error {
		v, ok := lookup(key)
		if !ok {
			return nil
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = f
		return nil
	}
	integer := func(key string, dst *int) error {
		v, ok := lookup(key)
		if !ok {
			return nil
		}
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = i
		return nil
	}
	boolean := func(key string, dst *bool) error {
		v, ok := lookup(key)
		if !ok {
			return nil
		}
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = b
		return nil
	}
	steps := []func() error{
		func() error { return str("DEADZONE_LOG_LEVEL", &c.LogLevel) },
		func() error { return str("DEADZONE_MQTT_HOST", &c.MQTT.Host) },
		func() error { return integer("DEADZONE_MQTT_PORT", &c.MQTT.Port) },
		func() error { return str("DEADZONE_MQTT_USER", &c.MQTT.User) },
		func() error { return str("DEADZONE_MQTT_PASS", &c.MQTT.Pass) },
		func() error { return str("DEADZONE_MQTT_CLIENT_ID", &c.MQTT.ClientID) },
		func() error { return str("DEADZONE_SA_PREFIX", &c.MQTT.SAPrefix) },
		func() error { return str("DEADZONE_TOPIC_PREFIX", &c.MQTT.TopicPrefix) },
		func() error { return str("DEADZONE_HA_PREFIX", &c.MQTT.HAPrefix) },
		func() error { return boolean("DEADZONE_HA_DISCOVERY", &c.MQTT.HADiscovery) },
		func() error { return boolean("DEADZONE_STARLINK_ENABLED", &c.Starlink.Enabled) },
		func() error { return str("DEADZONE_STARLINK_ADDR", &c.Starlink.Addr) },
		func() error { return num("DEADZONE_STARLINK_DISH_WATTS", &c.Starlink.DishWatts) },
		func() error { return boolean("DEADZONE_FORECAST_ENABLED", &c.Forecast.Enabled) },
		func() error { return num("DEADZONE_LAT", &c.Forecast.Latitude) },
		func() error { return num("DEADZONE_LON", &c.Forecast.Longitude) },
		func() error { return num("DEADZONE_TILT_DEG", &c.Forecast.TiltDeg) },
		func() error { return num("DEADZONE_AZIMUTH_DEG", &c.Forecast.AzimuthDeg) },
		func() error { return num("DEADZONE_ARRAY_KWP", &c.Forecast.ArrayKWp) },
		func() error { return str("DEADZONE_FORECAST_CACHE", &c.Forecast.CachePath) },
		func() error { return num("DEADZONE_BATTERY_KWH", &c.Battery.CapacityKWh) },
		func() error { return num("DEADZONE_RESERVE_SOC", &c.Battery.ReserveSOCPct) },
		func() error { return num("DEADZONE_PARASITIC_W", &c.Battery.ParasiticW) },
		func() error { return num("DEADZONE_WORKSTATION_W", &c.Workstation.Watts) },
		func() error { return num("DEADZONE_CALL_LENGTH_MIN", &c.Workstation.CallLengthMin) },
		func() error { return num("DEADZONE_TARGET_HOURS", &c.Workstation.TargetHours) },
		func() error { return num("DEADZONE_STALE_AFTER_S", &c.Thresholds.StaleAfterS) },
		func() error { return str("DEADZONE_HTTP_LISTEN", &c.HTTP.Listen) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

// Validate rejects configurations the engine cannot reason about.
func (c *Config) Validate() error {
	var errs []error
	if c.MQTT.Host == "" {
		errs = append(errs, errors.New("mqtt.host is required"))
	}
	if c.MQTT.Port <= 0 || c.MQTT.Port > 65535 {
		errs = append(errs, fmt.Errorf("mqtt.port %d out of range", c.MQTT.Port))
	}
	if c.Battery.CapacityKWh <= 0 {
		errs = append(errs, errors.New("battery.capacity_kwh must be > 0"))
	}
	if c.Battery.ReserveSOCPct < 0 || c.Battery.ReserveSOCPct >= 100 {
		errs = append(errs, errors.New("battery.reserve_soc_pct must be in [0,100)"))
	}
	if c.Workstation.Watts < 0 {
		errs = append(errs, errors.New("workstation.watts must be >= 0"))
	}
	if c.Forecast.Enabled {
		if c.Forecast.Latitude < -90 || c.Forecast.Latitude > 90 {
			errs = append(errs, errors.New("forecast.latitude out of range"))
		}
		if c.Forecast.Longitude < -180 || c.Forecast.Longitude > 180 {
			errs = append(errs, errors.New("forecast.longitude out of range"))
		}
		if c.Forecast.Latitude == 0 && c.Forecast.Longitude == 0 {
			errs = append(errs, errors.New("forecast.latitude/longitude are required when forecast.enabled (set forecast.enabled: false to skip)"))
		}
	}
	if c.Thresholds.StaleAfterS <= 0 {
		errs = append(errs, errors.New("thresholds.stale_after_s must be > 0"))
	}
	for i, s := range c.Shed {
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("shed[%d].name is required", i))
		}
		if s.Watts <= 0 {
			errs = append(errs, fmt.Errorf("shed[%d].watts must be > 0", i))
		}
	}
	return errors.Join(errs...)
}

// StaleAfter is the staleness window as a Duration.
func (c *Config) StaleAfter() time.Duration {
	return time.Duration(c.Thresholds.StaleAfterS * float64(time.Second))
}

// BrokerURL is the paho-style tcp:// URL.
func (c *MQTT) BrokerURL() string {
	return fmt.Sprintf("tcp://%s:%d", c.Host, c.Port)
}
