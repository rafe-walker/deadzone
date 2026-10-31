// Package model holds the shared data types that flow between sources, the
// engine, and the outputs. The JSON shape of State is the public contract
// served at GET /api/state and published retained on <prefix>/state.
package model

import "time"

// SOCSource describes where the state-of-charge number came from. It drives
// the trust band the engine applies to hours-remaining estimates.
type SOCSource string

const (
	// SOCInverterVoltage means the inverter estimated SOC from battery
	// voltage (PI30-class units with "Use inverter values" in Solar
	// Assistant). Flat LiFePO4 curves make this unreliable between roughly
	// 30 % and 90 %.
	SOCInverterVoltage SOCSource = "inverter_voltage"
	// SOCBMS means a battery_N device published SOC, i.e. a coulomb-counting
	// BMS is being read.
	SOCBMS SOCSource = "bms"
	// SOCUnknown means no SOC has been seen yet.
	SOCUnknown SOCSource = "unknown"
)

// Trust is the coarse label published to deadzone/power/soc_trust.
type Trust string

const (
	TrustHigh   Trust = "high"
	TrustMedium Trust = "medium"
	TrustLow    Trust = "low"
)

// Inverter is one inverter_N device group from Solar Assistant.
type Inverter struct {
	ID        string    `json:"id"`
	LoadW     *float64  `json:"load_w,omitempty"`
	LoadPct   *float64  `json:"load_pct,omitempty"`
	PVW       *float64  `json:"pv_w,omitempty"`
	PVV       *float64  `json:"pv_v,omitempty"`
	BattV     *float64  `json:"batt_v,omitempty"`
	BattA     *float64  `json:"batt_a,omitempty"`
	GridW     *float64  `json:"grid_w,omitempty"`
	GridV     *float64  `json:"grid_v,omitempty"`
	ACOutV    *float64  `json:"ac_out_v,omitempty"`
	ACOutHz   *float64  `json:"ac_out_hz,omitempty"`
	TempC     *float64  `json:"temp_c,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Serial    string    `json:"serial,omitempty"`
	ChargerP  string    `json:"charger_source_priority,omitempty"`
	OutputP   string    `json:"output_source_priority,omitempty"`
	ShutdownV *float64  `json:"shutdown_battery_v,omitempty"`
	Updated   time.Time `json:"updated"`
}

// Pack is one battery_N device group (only present when Solar Assistant is
// reading a BMS; absent on voltage-SOC benches).
type Pack struct {
	ID       string    `json:"id"`
	SOCPct   *float64  `json:"soc_pct,omitempty"`
	V        *float64  `json:"v,omitempty"`
	A        *float64  `json:"a,omitempty"`
	W        *float64  `json:"w,omitempty"`
	TempC    *float64  `json:"temp_c,omitempty"`
	CellMinV *float64  `json:"cell_min_v,omitempty"`
	CellMaxV *float64  `json:"cell_max_v,omitempty"`
	Updated  time.Time `json:"updated"`
}

// Power is the fused view of the solar/battery system.
type Power struct {
	SOCPct      *float64   `json:"soc_pct,omitempty"`
	SOCSource   SOCSource  `json:"soc_source"`
	SOCTrust    Trust      `json:"soc_trust"`
	BattV       *float64   `json:"batt_v,omitempty"`
	BattW       *float64   `json:"batt_w,omitempty"` // + charging, - discharging (SA convention varies; see parser)
	PVW         *float64   `json:"pv_w,omitempty"`
	LoadW       *float64   `json:"load_w,omitempty"`
	LoadEMAW    *float64   `json:"load_ema_w,omitempty"`
	GridW       *float64   `json:"grid_w,omitempty"`
	Inverters   []Inverter `json:"inverters,omitempty"`
	Packs       []Pack     `json:"packs,omitempty"`
	UsableWh    float64    `json:"usable_wh_now"`
	ReserveSOC  float64    `json:"reserve_soc_pct"`
	CapacityWh  float64    `json:"capacity_wh"`
	Stale       bool       `json:"stale"`
	LastMessage time.Time  `json:"last_message"`
	Caveats     []string   `json:"caveats,omitempty"`
}

// Outage is one entry from Starlink get_history.outages.
type Outage struct {
	Cause     string    `json:"cause"`
	Start     time.Time `json:"start"`
	DurationS float64   `json:"dur_s"`
	DidSwitch bool      `json:"did_switch"`
}

// Starlink is the subset of dish telemetry the engine cares about.
type Starlink struct {
	Present            bool      `json:"present"`
	Reachable          bool      `json:"reachable"`
	State              string    `json:"state,omitempty"`
	HardwareVersion    string    `json:"hardware_version,omitempty"`
	SoftwareVersion    string    `json:"software_version,omitempty"`
	UptimeS            *float64  `json:"uptime_s,omitempty"`
	DropNow            *float64  `json:"drop_now,omitempty"`
	LatencyNowMs       *float64  `json:"lat_ms_now,omitempty"`
	Drop5m             *float64  `json:"drop_5m,omitempty"`
	LatP50Ms           *float64  `json:"lat_ms_p50,omitempty"`
	LatP90Ms           *float64  `json:"lat_ms_p90,omitempty"`
	JitterMs           *float64  `json:"jitter_ms,omitempty"`
	DownlinkBps        *float64  `json:"downlink_bps,omitempty"`
	UplinkBps          *float64  `json:"uplink_bps,omitempty"`
	ObstructedNow      *bool     `json:"obstructed_now,omitempty"`
	FractionObstructed *float64  `json:"fraction_obstructed,omitempty"`
	ObstructionDurS    *float64  `json:"obstruction_dur_s,omitempty"`
	ObstructionIntvlS  *float64  `json:"obstruction_interval_s,omitempty"`
	Alerts             []string  `json:"alerts"`
	Outages15m         []Outage  `json:"outages_15m"`
	PowerW             *float64  `json:"power_w,omitempty"`
	SNRAboveNoiseFloor *bool     `json:"snr_above_noise_floor,omitempty"`
	HistorySamples     int       `json:"history_samples"`
	Updated            time.Time `json:"updated"`
	Error              string    `json:"error,omitempty"`
}

// Probe is the router-agnostic active probe summary over the trailing window.
type Probe struct {
	Enabled       bool       `json:"enabled"`
	Targets       []string   `json:"targets"`
	Loss60s       *float64   `json:"loss_60s,omitempty"`
	RTTP50Ms      *float64   `json:"rtt_ms_p50,omitempty"`
	RTTP90Ms      *float64   `json:"rtt_ms_p90,omitempty"`
	JitterMs      *float64   `json:"jitter_ms,omitempty"`
	Samples       int        `json:"samples"`
	PathChangedAt *time.Time `json:"path_changed_at,omitempty"`
	Updated       time.Time  `json:"updated"`
}

// Uplink groups every internet-side input.
type Uplink struct {
	Primary  string   `json:"primary"`
	Starlink Starlink `json:"starlink"`
	Cellular struct {
		Present bool `json:"present"`
	} `json:"cellular"`
	Probe Probe `json:"probe"`
}

// ForecastStep is one 15-minute slot of expected PV output.
type ForecastStep struct {
	Time   time.Time `json:"t"`
	GTI    float64   `json:"gti_wm2"`
	PVW    float64   `json:"pv_w"`
	Precip float64   `json:"precip_mm"`
}

// Forecast is the Open-Meteo derived view plus the learned performance ratio.
type Forecast struct {
	Available      bool           `json:"available"`
	FromCache      bool           `json:"from_cache"`
	FetchedAt      time.Time      `json:"fetched_at,omitempty"`
	PVWhNext6h     float64        `json:"pv_wh_next_6h"`
	PVWNowExpected float64        `json:"pv_w_now_expected"`
	PrecipMMNext2h float64        `json:"precip_mm_next_2h"`
	PRLearned      float64        `json:"pr_learned"`
	PRSamples      int            `json:"pr_samples"`
	Steps          []ForecastStep `json:"steps,omitempty"`
	Error          string         `json:"error,omitempty"`
}

// Light is the traffic-light verdict.
type Light string

const (
	Green Light = "green"
	Amber Light = "amber"
	Red   Light = "red"
)

// Rank orders lights so min() is well-defined (green best).
func (l Light) Rank() int {
	switch l {
	case Green:
		return 0
	case Amber:
		return 1
	default:
		return 2
	}
}

// Worse returns the worse of two lights.
func Worse(a, b Light) Light {
	if a.Rank() >= b.Rank() {
		return a
	}
	return b
}

// ShedItem is one load the operator could turn off, with the hours gained.
type ShedItem struct {
	Load     string  `json:"load"`
	W        float64 `json:"w"`
	GainH    float64 `json:"gain_h"`
	Control  string  `json:"control,omitempty"`
	Priority int     `json:"priority"`
}

// Verdict is the engine output.
type Verdict struct {
	CallOK        Light    `json:"call_ok"`
	UplinkLight   Light    `json:"uplink_light"`
	PowerLight    Light    `json:"power_light"`
	CallReasons   []string `json:"call_reasons"`
	CallLengthMin float64  `json:"call_length_min"`
	// HoursWorkstation is the conservative (PV = 0) estimate.
	HoursWorkstation float64 `json:"hours_workstation"`
	// HoursLow/High widen the conservative estimate by the SOC trust band.
	HoursLow  float64 `json:"hours_workstation_low"`
	HoursHigh float64 `json:"hours_workstation_high"`
	// HoursWithPV integrates the forecast until SOC hits reserve.
	HoursWithPV  float64    `json:"hours_workstation_with_pv"`
	UntilReserve *time.Time `json:"until,omitempty"`
	NetLoadW     float64    `json:"net_load_w"`
	Shed         []ShedItem `json:"shed"`
	ShedTargetH  float64    `json:"shed_target_h"`
	UplinkScore  int        `json:"uplink_score"`
	Caveats      []string   `json:"caveats,omitempty"`
}

// State is the complete snapshot.
type State struct {
	TS       time.Time `json:"ts"`
	Version  string    `json:"version"`
	Power    Power     `json:"power"`
	Uplink   Uplink    `json:"uplink"`
	Forecast Forecast  `json:"forecast"`
	Verdict  Verdict   `json:"verdict"`
	Actions  struct {
		Enabled bool   `json:"enabled"`
		Mode    string `json:"mode"` // "disabled" | "dry-run" | "live"
	} `json:"actions"`
}

// F is a helper to take the address of a float literal.
func F(v float64) *float64 { return &v }

// B is a helper to take the address of a bool literal.
func B(v bool) *bool { return &v }
