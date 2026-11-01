// Package engine implements the three questions as pure functions:
//
//	Q2 HoursAtSOC   — hours of workstation at current SOC (conservative PV=0
//	                  and forecast-integrated), with a trust band.
//	Q1 CallVerdict  — traffic light = worse(uplink gate, power gate).
//	Q3 ShedList     — which loads to turn off before a call and the hours
//	                  each one buys.
//
// Nothing here touches the network or the clock; callers pass `now`.
package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/model"
)

// Unknown is the gate result when the data needed to decide is missing or
// stale. It never becomes the published call_ok (that stays green/amber/red);
// an unknown gate degrades the verdict to amber with an explanatory reason.
const Unknown model.Light = "unknown"

// MaxHours caps runtime estimates when net load is zero or negative (PV
// covering everything): "more than ten days" is the honest answer.
const MaxHours = 240.0

// Params are the operator-configured numbers the formulas need.
type Params struct {
	CapacityWh     float64
	ReserveSOCPct  float64
	ParasiticW     float64
	WorkstationW   float64
	DishW          float64
	CallLengthMin  float64
	CallMarginPct  float64
	RedSOCAboveRes float64
	TargetHours    float64
	StaleAfter     time.Duration
	Uplink         config.UplinkThresholds
	Shed           []config.ShedLoad
}

// ParamsFromConfig lifts the YAML config into engine parameters.
func ParamsFromConfig(c config.Config) Params {
	return Params{
		CapacityWh:     c.Battery.CapacityKWh * 1000,
		ReserveSOCPct:  c.Battery.ReserveSOCPct,
		ParasiticW:     c.Battery.ParasiticW,
		WorkstationW:   c.Workstation.Watts,
		DishW:          c.Starlink.DishWatts,
		CallLengthMin:  c.Workstation.CallLengthMin,
		CallMarginPct:  c.Thresholds.CallMarginPct,
		RedSOCAboveRes: c.Thresholds.RedSOCAboveRes,
		TargetHours:    c.Workstation.TargetHours,
		StaleAfter:     c.StaleAfter(),
		Uplink:         c.Thresholds.Uplink,
		Shed:           c.Shed,
	}
}

// Inputs are the fused source snapshots.
type Inputs struct {
	Power    model.Power
	Starlink model.Starlink
	Forecast model.Forecast
	Now      time.Time
}

// Hours is the Q2 result.
type Hours struct {
	Known        bool
	UsableWh     float64
	WorkW        float64 // P_work: load the workstation session represents
	NetW         float64 // P_net with PV = 0
	Conservative float64 // hours, PV = 0
	Low, High    float64 // trust band around Conservative
	WithPV       float64 // hours integrating the forecast
	Until        *time.Time
	Caveats      []string
}

// socBand is the ± SOC-percentage-point uncertainty by trust level.
func socBand(t model.Trust) float64 {
	switch t {
	case model.TrustHigh:
		return 3
	case model.TrustMedium:
		return 7
	default:
		return 15
	}
}

func usable(capWh, soc, reserve float64) float64 {
	e := capWh * (soc - reserve) / 100
	if e < 0 {
		return 0
	}
	return e
}

func hoursFor(e, netW float64) float64 {
	if netW <= 0 {
		if e <= 0 {
			return 0
		}
		return MaxHours
	}
	h := e / netW
	if h > MaxHours {
		return MaxHours
	}
	return h
}

// isStale reports whether the power snapshot is too old to act on.
func isStale(p model.Power, now time.Time, after time.Duration) bool {
	if p.Stale {
		return true
	}
	if p.LastMessage.IsZero() {
		return true
	}
	return now.Sub(p.LastMessage) > after
}

// HoursAtSOC answers Q2.
func HoursAtSOC(in Inputs, prm Params) Hours {
	var h Hours
	p := in.Power
	if p.SOCPct == nil {
		h.Caveats = append(h.Caveats, "soc_unknown")
		return h
	}
	if isStale(p, in.Now, prm.StaleAfter) {
		h.Caveats = append(h.Caveats, "power_data_stale")
		return h
	}
	if prm.CapacityWh <= 0 {
		h.Caveats = append(h.Caveats, "battery_capacity_not_configured")
		return h
	}
	soc := *p.SOCPct
	h.UsableWh = usable(prm.CapacityWh, soc, prm.ReserveSOCPct)

	// P_work: trailing load average; if the workstation is evidently off
	// (load below its profile) assume it will be turned on for the session.
	load := 0.0
	switch {
	case p.LoadEMAW != nil:
		load = *p.LoadEMAW
	case p.LoadW != nil:
		load = *p.LoadW
	default:
		h.Caveats = append(h.Caveats, "load_unknown_using_workstation_profile")
	}
	if load < prm.WorkstationW {
		load = prm.WorkstationW
		h.Caveats = append(h.Caveats, "workstation_profile_applied")
	}
	h.WorkW = load
	h.NetW = load + prm.ParasiticW
	h.Known = true
	h.Conservative = hoursFor(h.UsableWh, h.NetW)

	band := socBand(p.SOCTrust)
	h.Low = hoursFor(usable(prm.CapacityWh, soc-band, prm.ReserveSOCPct), h.NetW)
	h.High = hoursFor(usable(prm.CapacityWh, soc+band, prm.ReserveSOCPct), h.NetW)
	if p.SOCTrust == model.TrustLow {
		h.Caveats = append(h.Caveats, "soc_trust_low")
	}

	h.WithPV = integrateForecast(h.UsableWh, h.NetW, prm.CapacityWh*(100-prm.ReserveSOCPct)/100, in.Forecast, in.Now)
	if h.WithPV < MaxHours {
		u := in.Now.Add(time.Duration(h.WithPV * float64(time.Hour)))
		h.Until = &u
	}
	return h
}

// integrateForecast steps through 15-minute PV slots until the usable
// energy is gone. Energy is capped at full (cannot store more than the
// pack holds). Past the forecast horizon PV is assumed zero.
func integrateForecast(e, netW, fullWh float64, f model.Forecast, now time.Time) float64 {
	if !f.Available || len(f.Steps) == 0 {
		return hoursFor(e, netW)
	}
	elapsed := 0.0
	t := now
	for _, st := range f.Steps {
		if st.Time.Add(15 * time.Minute).Before(now) {
			continue
		}
		if elapsed >= MaxHours {
			return MaxHours
		}
		// Slot may be partially elapsed.
		end := st.Time.Add(15 * time.Minute)
		dt := end.Sub(t).Hours()
		if dt <= 0 {
			continue
		}
		pnet := netW - st.PVW
		if pnet > 0 && e <= pnet*dt {
			return elapsed + e/pnet
		}
		e -= pnet * dt
		if e > fullWh {
			e = fullWh
		}
		elapsed += dt
		t = end
	}
	return math.Min(MaxHours, elapsed+hoursFor(e, netW))
}

// Gate is one side of the Q1 verdict.
type Gate struct {
	Light   model.Light
	Reasons []string
	Score   int
}

func (g *Gate) reason(format string, args ...any) {
	g.Reasons = append(g.Reasons, fmt.Sprintf(format, args...))
}

func hasAlert(alerts []string, name string) bool {
	for _, a := range alerts {
		if a == name {
			return true
		}
	}
	return false
}

// UplinkGate scores the Starlink side of Q1.
func UplinkGate(s model.Starlink, f model.Forecast, th config.UplinkThresholds, now time.Time) Gate {
	g := Gate{Light: model.Green, Reasons: []string{}, Score: 100}
	if !s.Present {
		g.Light = Unknown
		g.Score = 0
		g.reason("starlink_not_configured")
		return g
	}
	if !s.Reachable {
		g.Light = Unknown
		g.Score = 0
		if s.Error != "" {
			g.reason("starlink_unknown: %s", s.Error)
		} else {
			g.reason("starlink_unknown")
		}
		return g
	}

	// Hard reds.
	if s.State != "" && s.State != "CONNECTED" {
		g.Light = model.Red
		g.Score = 0
		g.reason("state=%s", s.State)
	}
	if hasAlert(s.Alerts, "thermal_shutdown") {
		g.Light = model.Red
		g.Score = 0
		g.reason("thermal_shutdown")
	}
	if s.ObstructedNow != nil && *s.ObstructedNow {
		g.Light = model.Red
		g.Score -= 50
		g.reason("currently_obstructed")
	}
	// Account/policy reds. A disabled dish keeps answering pings and keeps
	// reporting CONNECTED, so the ping numbers alone would say GO.
	if disabled(s.DisablementCode) {
		g.Light = model.Red
		g.Score = 0
		g.reason("disablement_code=%s", s.DisablementCode)
	}
	if s.Stowed != nil && *s.Stowed {
		g.Light = model.Red
		g.Score = 0
		g.reason("stowed")
	}
	if s.OverageRateLimited != nil && *s.OverageRateLimited {
		g.Light = model.Red
		g.Score = 0
		g.reason("overage_rate_limited")
	}
	for _, rl := range []struct{ dir, why string }{{"dl", s.RateLimitDL}, {"ul", s.RateLimitUL}} {
		switch rl.why {
		case "", "UNKNOWN", "NO_LIMIT":
		case "OVERAGE_LIMIT":
			g.Light = model.Red
			g.Score = 0
			g.reason("%s_rate_limit=%s", rl.dir, rl.why)
		default:
			// POLICY_LIMIT, USER_CUSTOM_LIMIT, LOW_SPEED_POLICY_LIMIT: the
			// link is capped but usually still call-capable.
			g.reason("%s_rate_limit=%s", rl.dir, rl.why)
			if g.Light == model.Green {
				g.Light = model.Amber
			}
			g.Score -= 15
		}
	}
	if s.HardwareSelfTest == "FAILED" {
		g.reason("hardware_self_test=FAILED")
		if g.Light == model.Green {
			g.Light = model.Amber
		}
		g.Score -= 10
	}

	// Numbers: prefer the 5-minute history, fall back to the instantaneous
	// status values.
	drop, p90, jitter := s.Drop5m, s.LatP90Ms, s.JitterMs
	if drop == nil {
		drop = s.DropNow
		p90 = s.LatencyNowMs
		if drop != nil {
			g.reason("history_unavailable_using_instant")
		}
	}
	if drop == nil {
		g.reason("no_uplink_samples")
		if g.Light == model.Green {
			g.Light = Unknown
			g.Score = 0
		}
		return g
	}

	long, short := 0, 0
	cutoff := now.Add(-5 * time.Minute)
	for _, o := range s.Outages15m {
		if !o.Start.IsZero() && o.Start.Before(cutoff) {
			continue
		}
		if o.DurationS >= th.OutageS {
			long++
		} else {
			short++
		}
	}

	level := model.Green
	worse := func(l model.Light) {
		if l.Rank() > level.Rank() {
			level = l
		}
	}
	classify := func(name string, v *float64, green, amber float64, unit string) {
		if v == nil {
			return
		}
		switch {
		case *v <= green:
			g.reason("%s=%s<=%s", name, fmtNum(*v, unit), fmtNum(green, unit))
		case *v <= amber:
			worse(model.Amber)
			g.reason("%s=%s>%s", name, fmtNum(*v, unit), fmtNum(green, unit))
		default:
			worse(model.Red)
			g.reason("%s=%s>%s", name, fmtNum(*v, unit), fmtNum(amber, unit))
		}
	}
	classify("drop_5m", drop, th.GreenDrop, th.AmberDrop, "")
	classify("lat_p90", p90, th.GreenP90Ms, th.AmberP90Ms, "ms")
	classify("jitter", jitter, th.GreenJitterMs, th.AmberJitterMs, "ms")
	switch {
	case long == 0:
		if short > 0 {
			g.reason("short_outages_5m=%d", short)
		}
	case long == 1:
		worse(model.Amber)
		g.reason("outage>=%.0fs_in_5m", th.OutageS)
	default:
		worse(model.Red)
		g.reason("outages>=%.0fs_in_5m=%d", th.OutageS, long)
	}

	// Score (0-100), independent of the light so HA can graph it.
	g.Score -= int(math.Round(*drop * 1000))
	if p90 != nil && *p90 > 50 {
		g.Score -= int((*p90 - 50) / 4)
	}
	if jitter != nil {
		g.Score -= int(*jitter)
	}
	g.Score -= 10*long + 3*short

	// Storm-risk caps.
	capAmber := func(why string) {
		worse(model.Amber)
		g.reason("storm_risk:%s", why)
		g.Score -= 10
	}
	if hasAlert(s.Alerts, "is_heating") {
		capAmber("is_heating")
	}
	if hasAlert(s.Alerts, "lower_signal_than_predicted") {
		capAmber("lower_signal_than_predicted")
	}
	if f.Available && f.PrecipMMNext2h > th.StormPrecipMM {
		capAmber(fmt.Sprintf("precip_2h=%.1fmm", f.PrecipMMNext2h))
	}

	if g.Light == model.Green {
		g.Light = level
	} else if level.Rank() > g.Light.Rank() {
		g.Light = level
	}
	if g.Score < 0 {
		g.Score = 0
	}
	if g.Score > 100 {
		g.Score = 100
	}
	return g
}

// disabled reports whether a UtDisablementCode enum name means the dish is
// not allowed to pass traffic. Empty, UNKNOWN and UNKNOWN_STATE (the dish
// still loading state, or firmware without the field) are not reds.
func disabled(code string) bool {
	switch code {
	case "", "OKAY", "UNKNOWN", "UNKNOWN_STATE":
		return false
	}
	return true
}

func fmtNum(v float64, unit string) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f%s", v, unit)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".") + unit
}

// PowerGate scores the battery side of Q1 using the Q2 result.
func PowerGate(in Inputs, h Hours, prm Params) Gate {
	g := Gate{Light: model.Green, Reasons: []string{}}
	p := in.Power
	if !h.Known {
		g.Light = Unknown
		g.Reasons = append(g.Reasons, h.Caveats...)
		if len(g.Reasons) == 0 {
			g.reason("power_unknown")
		}
		return g
	}
	soc := *p.SOCPct
	for _, inv := range p.Inverters {
		if strings.Contains(strings.ToLower(inv.Mode), "fault") {
			g.Light = model.Red
			g.reason("%s_mode=%s", inv.ID, inv.Mode)
		}
	}
	if soc <= prm.ReserveSOCPct+prm.RedSOCAboveRes {
		g.Light = model.Red
		g.reason("soc=%.0f%%<=reserve+%.0f", soc, prm.RedSOCAboveRes)
		return g
	}
	dish, dishSrc := DishWatts(in.Starlink, prm.DishW)
	callH := prm.CallLengthMin / 60
	need := (h.WorkW + dish) * callH * (1 + prm.CallMarginPct/100)
	if h.UsableWh >= need {
		g.reason("usable=%.0fWh>=call_budget=%.0fWh", h.UsableWh, need)
	} else {
		if g.Light == model.Green {
			g.Light = model.Amber
		}
		g.reason("usable=%.0fWh<call_budget=%.0fWh", h.UsableWh, need)
	}
	g.reason("dish_w=%.0f(%s)", dish, dishSrc)
	if p.SOCTrust == model.TrustLow {
		g.reason("soc_trust=low")
	}
	return g
}

// DishWatts picks the dish draw for the call budget: the 5-minute mean
// from get_history.power_in when the dish reports it, else the newest
// sample, else the configured starlink_dish_watts. The second value names
// the source ("dish_mean_5m", "dish_now", "config").
func DishWatts(s model.Starlink, configured float64) (float64, string) {
	if s.PowerMeanW != nil && *s.PowerMeanW > 0 {
		return *s.PowerMeanW, "dish_mean_5m"
	}
	if s.PowerW != nil && *s.PowerW > 0 {
		return *s.PowerW, "dish_now"
	}
	return configured, "config"
}

// ShedList answers Q3: loads to turn off, most effective first, until the
// workstation hours reach target. Priority 1 is shed first.
func ShedList(loads []config.ShedLoad, usableWh, netW, targetH float64) []model.ShedItem {
	out := []model.ShedItem{}
	if len(loads) == 0 || usableWh <= 0 {
		return out
	}
	sorted := append([]config.ShedLoad(nil), loads...)
	key := func(l config.ShedLoad) float64 {
		p := float64(l.Priority)
		if p <= 0 {
			p = 1
		}
		return l.Watts / p
	}
	sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i]) > key(sorted[j]) })

	base := hoursFor(usableWh, netW)
	if base >= targetH {
		return out
	}
	cur := netW
	hours := base
	for _, l := range sorted {
		after := cur - l.Watts
		gain := hoursFor(usableWh, after) - hours
		if gain < 0 {
			gain = 0
		}
		out = append(out, model.ShedItem{
			Load:     l.Name,
			W:        l.Watts,
			GainH:    round2(gain),
			Control:  l.Control,
			Priority: l.Priority,
		})
		cur = after
		hours = hoursFor(usableWh, cur)
		if hours >= targetH {
			break
		}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Evaluate runs Q2, Q1 and Q3 and assembles the Verdict.
func Evaluate(in Inputs, prm Params) model.Verdict {
	h := HoursAtSOC(in, prm)
	up := UplinkGate(in.Starlink, in.Forecast, prm.Uplink, in.Now)
	pw := PowerGate(in, h, prm)

	v := model.Verdict{
		UplinkLight:      up.Light,
		PowerLight:       pw.Light,
		CallReasons:      []string{},
		CallLengthMin:    prm.CallLengthMin,
		HoursWorkstation: round2(h.Conservative),
		HoursLow:         round2(h.Low),
		HoursHigh:        round2(h.High),
		HoursWithPV:      round2(h.WithPV),
		UntilReserve:     h.Until,
		NetLoadW:         round2(h.NetW),
		ShedTargetH:      prm.TargetHours,
		UplinkScore:      up.Score,
		Caveats:          append([]string{}, h.Caveats...),
	}
	v.CallOK = Combine(up.Light, pw.Light)
	for _, r := range up.Reasons {
		v.CallReasons = append(v.CallReasons, "uplink:"+r)
	}
	for _, r := range pw.Reasons {
		v.CallReasons = append(v.CallReasons, "power:"+r)
	}
	if h.Known {
		v.Shed = ShedList(prm.Shed, h.UsableWh, h.NetW, prm.TargetHours)
	} else {
		v.Shed = []model.ShedItem{}
	}
	return v
}

// Combine folds the two gates into the published light. Unknown never
// escapes: it reads as amber unless the other gate is already red.
func Combine(uplink, power model.Light) model.Light {
	if uplink == model.Red || power == model.Red {
		return model.Red
	}
	if uplink == Unknown || power == Unknown {
		return model.Amber
	}
	return model.Worse(uplink, power)
}

// ExitCode maps a light to the `deadzone check` exit status.
func ExitCode(l model.Light) int {
	switch l {
	case model.Green:
		return 0
	case model.Amber:
		return 1
	default:
		return 2
	}
}

// PRLearner fits the performance ratio from observed PV against forecast
// GTI. It is an EMA over samples taken when irradiance is meaningful, so a
// cloudy morning does not drag it to zero.
type PRLearner struct {
	ArrayKWp float64
	Alpha    float64 // EMA weight per sample, default 0.05
	MinGTI   float64 // ignore samples below this W/m², default 150
	pr       float64
	n        int
}

// NewPRLearner starts from a prior PR.
func NewPRLearner(arrayKWp, prior float64) *PRLearner {
	return &PRLearner{ArrayKWp: arrayKWp, Alpha: 0.05, MinGTI: 150, pr: prior}
}

// Observe feeds one (GTI, measured PV W) pair. Returns true if used.
func (l *PRLearner) Observe(gti, pvW float64) bool {
	if l.ArrayKWp <= 0 || gti < l.MinGTI || pvW < 0 {
		return false
	}
	expected := gti * l.ArrayKWp // W at PR = 1
	if expected <= 0 {
		return false
	}
	ratio := pvW / expected
	// Clipping and shading make >1.05 or <0.2 implausible; clamp rather
	// than discard so a mis-sized array still converges somewhere sane.
	ratio = math.Max(0.2, math.Min(1.05, ratio))
	if l.n == 0 {
		l.pr = ratio
	} else {
		l.pr += l.Alpha * (ratio - l.pr)
	}
	l.n++
	return true
}

// PR returns the current estimate and the number of samples behind it.
func (l *PRLearner) PR() (float64, int) { return l.pr, l.n }
