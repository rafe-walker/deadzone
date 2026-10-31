// Package solarassistant turns the Solar Assistant MQTT topic tree into a
// model.Power snapshot.
//
// Topic shape (Solar Assistant MQTT integration doc):
//
//	<prefix>/<device>/<metric>/state   e.g. solar_assistant/inverter_1/load_power/state
//
// Devices are total, inverter_N and battery_N. Payloads are bare values with
// no unit suffix. The exact metric set depends on the inverter driver; this
// parser maps the names it knows and keeps everything else in a raw map so
// nothing is lost.
package solarassistant

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/internal/source"
)

// Subscriber is the slice of the MQTT bus the source needs.
type Subscriber interface {
	Subscribe(topic string, handler func(topic string, payload []byte)) error
}

// Options tune the source.
type Options struct {
	// Prefix is the Solar Assistant topic prefix, default "solar_assistant".
	Prefix string
	// StaleAfter marks the snapshot stale when no message arrived for this
	// long. Default 30 s.
	StaleAfter time.Duration
	// LoadEMATau is the time constant of the load exponential moving
	// average. Default 10 min.
	LoadEMATau time.Duration
	// CapacityWh and ReserveSOCPct let the snapshot carry usable energy.
	CapacityWh    float64
	ReserveSOCPct float64
	// BatteryPositiveIsCharging flips the sign of total/battery_power so
	// that the snapshot always uses +charging / -discharging.
	BatteryPositiveIsCharging bool
	// Now lets tests control the clock.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.Prefix == "" {
		o.Prefix = "solar_assistant"
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 30 * time.Second
	}
	if o.LoadEMATau <= 0 {
		o.LoadEMATau = 10 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

type metric struct {
	raw     string
	num     *float64
	updated time.Time
}

// Source is the Solar Assistant MQTT source.
type Source struct {
	opts Options
	sub  Subscriber

	mu       sync.RWMutex
	devices  map[string]map[string]metric // device -> metric -> value
	lastMsg  time.Time
	loadEMA  *float64
	loadEMAT time.Time
	count    int64
}

// New builds a source. sub may be nil for fixture-driven use (call Handle).
func New(sub Subscriber, opts Options) *Source {
	opts.defaults()
	return &Source{opts: opts, sub: sub, devices: map[string]map[string]metric{}}
}

// Name implements source.Source.
func (s *Source) Name() string { return "solarassistant" }

// Start subscribes to <prefix>/#. It returns immediately; messages arrive on
// the bus's goroutines.
func (s *Source) Start(ctx context.Context) error {
	if s.sub == nil {
		return nil
	}
	return s.sub.Subscribe(s.opts.Prefix+"/#", func(topic string, payload []byte) {
		s.Handle(topic, payload)
	})
}

// Handle ingests one MQTT message. Exported so fixtures and other buses can
// feed the parser directly.
func (s *Source) Handle(topic string, payload []byte) {
	device, name, ok := s.splitTopic(topic)
	if !ok {
		return
	}
	now := s.opts.Now()
	raw := strings.TrimSpace(string(payload))
	m := metric{raw: raw, updated: now}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		m.num = &f
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.devices[device]
	if dev == nil {
		dev = map[string]metric{}
		s.devices[device] = dev
	}
	dev[name] = m
	s.lastMsg = now
	s.count++
	if device == "total" && name == "load_power" && m.num != nil {
		s.updateEMA(*m.num, now)
	}
}

// MessageCount reports how many messages were accepted (for diagnostics).
func (s *Source) MessageCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

func (s *Source) updateEMA(v float64, now time.Time) {
	if s.loadEMA == nil {
		s.loadEMA = &v
		s.loadEMAT = now
		return
	}
	dt := now.Sub(s.loadEMAT).Seconds()
	if dt <= 0 {
		return
	}
	alpha := 1 - math.Exp(-dt/s.opts.LoadEMATau.Seconds())
	ema := *s.loadEMA + alpha*(v-*s.loadEMA)
	s.loadEMA = &ema
	s.loadEMAT = now
}

func (s *Source) splitTopic(topic string) (device, name string, ok bool) {
	if !strings.HasPrefix(topic, s.opts.Prefix+"/") {
		return "", "", false
	}
	rest := strings.TrimPrefix(topic, s.opts.Prefix+"/")
	parts := strings.Split(rest, "/")
	// <device>/<metric>/state ; tolerate <device>/<metric> without /state.
	switch {
	case len(parts) == 3 && parts[2] == "state":
		return parts[0], parts[1], true
	case len(parts) == 2:
		return parts[0], parts[1], true
	}
	return "", "", false
}

// get returns a numeric metric for device if present.
func (s *Source) get(device, name string) *float64 {
	dev := s.devices[device]
	if dev == nil {
		return nil
	}
	m, ok := dev[name]
	if !ok || m.num == nil {
		return nil
	}
	v := *m.num
	return &v
}

func (s *Source) getStr(device, name string) string {
	dev := s.devices[device]
	if dev == nil {
		return ""
	}
	return dev[name].raw
}

// first returns the first metric found among candidate names.
func (s *Source) first(device string, names ...string) *float64 {
	for _, n := range names {
		if v := s.get(device, n); v != nil {
			return v
		}
	}
	return nil
}

func (s *Source) deviceIDs(prefix string) []string {
	var ids []string
	for d := range s.devices {
		if strings.HasPrefix(d, prefix) {
			ids = append(ids, d)
		}
	}
	sort.Strings(ids)
	return ids
}

func sum(vals ...*float64) *float64 {
	var total float64
	n := 0
	for _, v := range vals {
		if v != nil {
			total += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return &total
}

func mean(vals ...*float64) *float64 {
	s := sum(vals...)
	if s == nil {
		return nil
	}
	n := 0
	for _, v := range vals {
		if v != nil {
			n++
		}
	}
	m := *s / float64(n)
	return &m
}

// Snapshot implements source.Source.
func (s *Source) Snapshot() source.Snapshot {
	p := s.Power()
	return source.Snapshot{Power: &p, Updated: p.LastMessage}
}

// Power builds the fused power view from everything seen so far.
func (s *Source) Power() model.Power {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.opts.Now()
	p := model.Power{
		SOCSource:   model.SOCUnknown,
		SOCTrust:    model.TrustLow,
		ReserveSOC:  s.opts.ReserveSOCPct,
		CapacityWh:  s.opts.CapacityWh,
		LastMessage: s.lastMsg,
	}
	if s.lastMsg.IsZero() {
		p.Stale = true
		p.Caveats = append(p.Caveats, "no Solar Assistant messages received yet")
		return p
	}
	if now.Sub(s.lastMsg) > s.opts.StaleAfter {
		p.Stale = true
		p.Caveats = append(p.Caveats, "Solar Assistant data stale for "+now.Sub(s.lastMsg).Truncate(time.Second).String())
	}

	// Inverters.
	var invLoad, invPV, invBattV, invBattA, invGrid []*float64
	for _, id := range s.deviceIDs("inverter_") {
		inv := model.Inverter{
			ID:        id,
			LoadW:     s.first(id, "load_power", "load_power_essential"),
			LoadPct:   s.first(id, "load_percentage"),
			PVW:       s.first(id, "pv_power", "pv_power_1"),
			PVV:       s.first(id, "pv_voltage", "pv_voltage_1"),
			BattV:     s.first(id, "battery_voltage"),
			BattA:     s.first(id, "battery_current"),
			GridW:     s.first(id, "grid_power", "grid_power_ld"),
			GridV:     s.first(id, "grid_voltage"),
			ACOutV:    s.first(id, "ac_output_voltage"),
			ACOutHz:   s.first(id, "ac_output_frequency"),
			TempC:     s.first(id, "temperature"),
			Mode:      s.getStr(id, "device_mode"),
			Serial:    s.getStr(id, "serial_number"),
			ChargerP:  s.getStr(id, "charger_source_priority"),
			OutputP:   s.getStr(id, "output_source_priority"),
			ShutdownV: s.first(id, "shutdown_battery_voltage"),
			Updated:   s.latest(id),
		}
		p.Inverters = append(p.Inverters, inv)
		invLoad = append(invLoad, inv.LoadW)
		invPV = append(invPV, inv.PVW)
		invBattV = append(invBattV, inv.BattV)
		invBattA = append(invBattA, inv.BattA)
		invGrid = append(invGrid, inv.GridW)
	}

	// Packs (only when SA reads a BMS).
	for _, id := range s.deviceIDs("battery_") {
		pk := model.Pack{
			ID:       id,
			SOCPct:   s.first(id, "state_of_charge", "battery_state_of_charge", "soc"),
			V:        s.first(id, "voltage"),
			A:        s.first(id, "current"),
			W:        s.first(id, "power"),
			TempC:    s.first(id, "temperature"),
			CellMinV: s.first(id, "cell_voltage_min", "min_cell_voltage"),
			CellMaxV: s.first(id, "cell_voltage_max", "max_cell_voltage"),
			Updated:  s.latest(id),
		}
		p.Packs = append(p.Packs, pk)
	}

	// Totals, with per-inverter fallbacks.
	p.LoadW = s.first("total", "load_power")
	if p.LoadW == nil {
		p.LoadW = sum(invLoad...)
	}
	p.PVW = s.first("total", "pv_power")
	if p.PVW == nil {
		p.PVW = sum(invPV...)
	}
	p.GridW = s.first("total", "grid_power")
	if p.GridW == nil {
		p.GridW = sum(invGrid...)
	}
	p.BattV = s.first("total", "battery_voltage")
	if p.BattV == nil {
		p.BattV = mean(invBattV...)
	}
	if bw := s.first("total", "battery_power"); bw != nil {
		v := *bw
		if !s.opts.BatteryPositiveIsCharging {
			v = -v
		}
		p.BattW = &v
	} else if a := sum(invBattA...); a != nil && p.BattV != nil {
		v := *a * *p.BattV
		p.BattW = &v
	}

	// SOC: BMS packs win, then total, then inverter mean.
	var packSOC []*float64
	for _, pk := range p.Packs {
		packSOC = append(packSOC, pk.SOCPct)
	}
	if soc := mean(packSOC...); soc != nil {
		p.SOCPct = soc
		p.SOCSource = model.SOCBMS
		p.SOCTrust = model.TrustHigh
	} else {
		soc := s.first("total", "battery_state_of_charge")
		if soc == nil {
			var invSOC []*float64
			for _, id := range s.deviceIDs("inverter_") {
				invSOC = append(invSOC, s.first(id, "battery_state_of_charge"))
			}
			soc = mean(invSOC...)
		}
		if soc != nil {
			p.SOCPct = soc
			p.SOCSource = model.SOCInverterVoltage
			p.SOCTrust = model.TrustLow
			p.Caveats = append(p.Caveats, "SOC is inverter voltage-estimated; LiFePO4 voltage is flat between ~30 % and ~90 %")
		}
	}

	if s.loadEMA != nil {
		v := *s.loadEMA
		p.LoadEMAW = &v
	}
	if p.SOCPct != nil && p.CapacityWh > 0 {
		usable := p.CapacityWh * (*p.SOCPct - p.ReserveSOC) / 100
		if usable < 0 {
			usable = 0
		}
		p.UsableWh = usable
	}
	return p
}

func (s *Source) latest(device string) time.Time {
	var t time.Time
	for _, m := range s.devices[device] {
		if m.updated.After(t) {
			t = m.updated
		}
	}
	return t
}

// Devices lists the device groups seen so far (sorted).
func (s *Source) Devices() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deviceIDs("")
}
