package bus

import (
	"encoding/json"
	"fmt"
)

// Entity describes one Home Assistant MQTT discovery config.
type Entity struct {
	Component  string // "sensor" | "binary_sensor"
	Key        string // object_id suffix, e.g. "verdict"
	Name       string
	Template   string // value_template over deadzone/state JSON
	Unit       string
	Icon       string
	Class      string // device_class
	StateCls   string // state_class
	Diagnostic bool
}

// Entities is the fixed v0.1 entity list, in publication order.
var Entities = []Entity{
	{Component: "sensor", Key: "verdict", Name: "Verdict", Template: "{{ value_json.verdict.call_ok }}", Icon: "mdi:traffic-light"},
	{Component: "binary_sensor", Key: "call_ok", Name: "Call OK", Template: "{{ value_json.verdict.call_ok == 'green' }}", Icon: "mdi:video", Class: "connectivity"},
	{Component: "sensor", Key: "hours_at_current_load", Name: "Hours at current load", Template: "{{ value_json.verdict.hours_workstation }}", Unit: "h", Icon: "mdi:timer-sand", StateCls: "measurement"},
	{Component: "sensor", Key: "hours_with_pv", Name: "Hours with PV forecast", Template: "{{ value_json.verdict.hours_workstation_with_pv }}", Unit: "h", Icon: "mdi:weather-sunny", StateCls: "measurement"},
	{Component: "sensor", Key: "battery_soc", Name: "Battery SOC", Template: "{{ value_json.power.soc_pct }}", Unit: "%", Class: "battery", StateCls: "measurement"},
	{Component: "sensor", Key: "pv_w", Name: "PV power", Template: "{{ value_json.power.pv_w }}", Unit: "W", Class: "power", StateCls: "measurement"},
	{Component: "sensor", Key: "load_w", Name: "Load power", Template: "{{ value_json.power.load_w }}", Unit: "W", Class: "power", StateCls: "measurement"},
	{Component: "sensor", Key: "starlink_pop_ping_ms", Name: "Starlink POP ping", Template: "{{ value_json.uplink.starlink.lat_ms_now }}", Unit: "ms", Class: "duration", StateCls: "measurement", Icon: "mdi:satellite-variant"},
	{Component: "sensor", Key: "starlink_downlink_mbps", Name: "Starlink downlink", Template: "{{ (value_json.uplink.starlink.downlink_bps / 1000000) | round(1) }}", Unit: "Mbit/s", Class: "data_rate", StateCls: "measurement"},
	{Component: "binary_sensor", Key: "starlink_obstructed", Name: "Starlink obstructed", Template: "{{ value_json.uplink.starlink.obstructed_now }}", Class: "problem"},
	{Component: "sensor", Key: "forecast_cloud_pct", Name: "Forecast cloud cover", Template: "{{ value_json.forecast.cloud_cover_pct }}", Unit: "%", Icon: "mdi:weather-cloudy", StateCls: "measurement"},
	{Component: "sensor", Key: "uplink_score", Name: "Uplink score", Template: "{{ value_json.verdict.uplink_score }}", Icon: "mdi:signal", StateCls: "measurement", Diagnostic: true},
	{Component: "sensor", Key: "starlink_drop_5m", Name: "Starlink drop rate 5m", Template: "{{ (value_json.uplink.starlink.drop_5m * 100) | round(2) }}", Unit: "%", Icon: "mdi:signal-off", StateCls: "measurement", Diagnostic: true},
	{Component: "sensor", Key: "shed_recommendation", Name: "Shed recommendation", Template: "{{ value_json.verdict.shed | map(attribute='load') | join(', ') if value_json.verdict.shed else 'none' }}", Icon: "mdi:power-plug-off"},
}

// DiscoveryTopic is where the config for an entity lives:
// <ha_prefix>/<component>/deadzone/<key>/config
func DiscoveryTopic(haPrefix string, e Entity) string {
	return fmt.Sprintf("%s/%s/deadzone/%s/config", haPrefix, e.Component, e.Key)
}

// DiscoveryMessages renders the HA MQTT discovery configs. All entities read
// the retained deadzone/state JSON through value_template, so there is one
// state topic and one availability topic to reason about.
func DiscoveryMessages(haPrefix, prefix, version string) ([]Message, error) {
	t := Topics{Prefix: prefix}
	device := map[string]any{
		"identifiers":  []string{"deadzone"},
		"name":         "deadzone",
		"manufacturer": "The Exiled Entrepreneur",
		"model":        "deadzone call-readiness daemon",
		"sw_version":   version,
	}
	var out []Message
	for _, e := range Entities {
		cfg := map[string]any{
			"name":                  e.Name,
			"unique_id":             "deadzone_" + e.Key,
			"object_id":             "deadzone_" + e.Key,
			"state_topic":           t.State(),
			"value_template":        e.Template,
			"availability_topic":    t.Availability(),
			"payload_available":     "online",
			"payload_not_available": "offline",
			"device":                device,
			"origin":                map[string]any{"name": "deadzone", "sw": version, "url": "https://github.com/rafe-walker/deadzone"},
		}
		if e.Component == "binary_sensor" {
			cfg["payload_on"] = "True"
			cfg["payload_off"] = "False"
		}
		if e.Unit != "" {
			cfg["unit_of_measurement"] = e.Unit
		}
		if e.Icon != "" {
			cfg["icon"] = e.Icon
		}
		if e.Class != "" {
			cfg["device_class"] = e.Class
		}
		if e.StateCls != "" {
			cfg["state_class"] = e.StateCls
		}
		if e.Diagnostic {
			cfg["entity_category"] = "diagnostic"
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, Message{Topic: DiscoveryTopic(haPrefix, e), Payload: b, Retain: true})
	}
	return out, nil
}
