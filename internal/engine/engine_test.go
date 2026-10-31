package engine

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/model"
)

var now = time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)

func params() Params {
	c := config.Default()
	c.Battery.CapacityKWh = 10.0 // 10 000 Wh, reserve 20 % -> 8 000 usable at 100 %
	c.Battery.ParasiticW = 50
	c.Workstation.Watts = 200
	c.Starlink.DishWatts = 50
	c.Shed = []config.ShedLoad{
		{Name: "water_heater", Watts: 1200, Priority: 1, Control: "switch.water_heater"},
		{Name: "well_pump", Watts: 800, Priority: 3},
		{Name: "fridge", Watts: 120, Priority: 5},
	}
	return ParamsFromConfig(c)
}

func fresh(soc, load, pv float64) model.Power {
	return model.Power{
		SOCPct:      model.F(soc),
		SOCSource:   model.SOCBMS,
		SOCTrust:    model.TrustHigh,
		LoadW:       model.F(load),
		LoadEMAW:    model.F(load),
		PVW:         model.F(pv),
		LastMessage: now.Add(-2 * time.Second),
		Inverters:   []model.Inverter{{ID: "inverter_1", Mode: "Battery"}, {ID: "inverter_2", Mode: "Battery"}},
	}
}

func goodDish() model.Starlink {
	return model.Starlink{
		Present: true, Reachable: true, State: "CONNECTED",
		Drop5m: model.F(0.005), LatP50Ms: model.F(35), LatP90Ms: model.F(60), JitterMs: model.F(8),
		ObstructedNow: model.B(false), Alerts: []string{}, Outages15m: []model.Outage{}, Updated: now,
	}
}

func night() model.Forecast {
	var steps []model.ForecastStep
	for i := 0; i < 48; i++ {
		steps = append(steps, model.ForecastStep{Time: now.Add(time.Duration(i) * 15 * time.Minute)})
	}
	return model.Forecast{Available: true, Steps: steps}
}

func sunny(pvW float64, hours int) model.Forecast {
	var steps []model.ForecastStep
	for i := 0; i < hours*4; i++ {
		steps = append(steps, model.ForecastStep{Time: now.Add(time.Duration(i) * 15 * time.Minute), PVW: pvW})
	}
	return model.Forecast{Available: true, Steps: steps}
}

func hasReason(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestHoursAtSOC(t *testing.T) {
	prm := params()
	cases := []struct {
		name         string
		power        model.Power
		forecast     model.Forecast
		wantKnown    bool
		wantCons     float64
		wantWithPV   float64
		wantCaveat   string
		wantLowBelow bool
	}{
		{
			name:  "night, SOC 70, load 450",
			power: fresh(70, 450, 0), forecast: night(),
			wantKnown: true,
			// usable = 10000*(70-20)/100 = 5000; net = 450+50 = 500 -> 10 h; night PV = 0 -> same
			wantCons: 10, wantWithPV: 10,
		},
		{
			name:  "SOC exactly at reserve",
			power: fresh(20, 300, 0), forecast: night(),
			wantKnown: true, wantCons: 0, wantWithPV: 0,
		},
		{
			name:  "PV covering load: net never positive",
			power: fresh(60, 300, 1500), forecast: sunny(1500, 12),
			wantKnown: true,
			// conservative ignores PV: 4000/350 = 11.43; with PV the pack fills
			// (capped at 8000 Wh usable) for the 12 h horizon, then 8000/350.
			wantCons: 4000.0 / 350, wantWithPV: 12 + 8000.0/350,
		},
		{
			name:     "stale > 30 s -> unknown",
			power:    func() model.Power { p := fresh(70, 450, 0); p.LastMessage = now.Add(-31 * time.Second); return p }(),
			forecast: night(), wantKnown: false, wantCaveat: "power_data_stale",
		},
		{
			name:     "stale flag from source -> unknown",
			power:    func() model.Power { p := fresh(70, 450, 0); p.Stale = true; return p }(),
			forecast: night(), wantKnown: false, wantCaveat: "power_data_stale",
		},
		{
			name:     "no SOC -> unknown",
			power:    func() model.Power { p := fresh(70, 450, 0); p.SOCPct = nil; return p }(),
			forecast: night(), wantKnown: false, wantCaveat: "soc_unknown",
		},
		{
			name:  "workstation off: profile applied",
			power: fresh(70, 80, 0), forecast: night(),
			wantKnown: true,
			// load 80 < 200 profile -> 200+50 = 250 -> 5000/250 = 20
			wantCons: 20, wantWithPV: 20, wantCaveat: "workstation_profile_applied",
		},
		{
			name: "low trust widens band",
			power: func() model.Power {
				p := fresh(70, 450, 0)
				p.SOCSource = model.SOCInverterVoltage
				p.SOCTrust = model.TrustLow
				return p
			}(),
			forecast: night(), wantKnown: true, wantCons: 10, wantWithPV: 10,
			wantCaveat: "soc_trust_low", wantLowBelow: true,
		},
		{
			name:  "no forecast at all falls back to conservative",
			power: fresh(70, 450, 0), forecast: model.Forecast{},
			wantKnown: true, wantCons: 10, wantWithPV: 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := HoursAtSOC(Inputs{Power: tc.power, Forecast: tc.forecast, Now: now}, prm)
			if h.Known != tc.wantKnown {
				t.Fatalf("known=%v want %v (caveats %v)", h.Known, tc.wantKnown, h.Caveats)
			}
			if tc.wantCaveat != "" && !hasReason(h.Caveats, tc.wantCaveat) {
				t.Errorf("caveats=%v want %q", h.Caveats, tc.wantCaveat)
			}
			if !tc.wantKnown {
				return
			}
			if math.Abs(h.Conservative-tc.wantCons) > 0.01 {
				t.Errorf("conservative=%v want %v", h.Conservative, tc.wantCons)
			}
			if math.Abs(h.WithPV-tc.wantWithPV) > 0.01 {
				t.Errorf("with_pv=%v want %v", h.WithPV, tc.wantWithPV)
			}
			if tc.wantLowBelow {
				// low trust: ±15 SOC points -> low = 10000*(55-20)/100/500 = 7, high = 13
				if math.Abs(h.Low-7) > 0.01 || math.Abs(h.High-13) > 0.01 {
					t.Errorf("band low/high = %v/%v want 7/13", h.Low, h.High)
				}
			} else if tc.wantCons > 0 && tc.wantCons < MaxHours && !(h.Low <= h.Conservative && h.Conservative <= h.High) {
				t.Errorf("band %v..%v should bracket %v", h.Low, h.High, h.Conservative)
			}
		})
	}
}

func TestIntegrateForecastPartialSun(t *testing.T) {
	prm := params()
	// 5000 Wh usable, net 500 W. Two hours of 300 W PV then dark:
	// first 2 h drain 200 W -> 400 Wh used -> 4600 left -> 9.2 h more -> 11.2 h total.
	f := sunny(300, 2)
	h := HoursAtSOC(Inputs{Power: fresh(70, 450, 300), Forecast: f, Now: now}, prm)
	if math.Abs(h.WithPV-11.2) > 0.01 {
		t.Fatalf("with_pv=%v want 11.2", h.WithPV)
	}
	if h.Until == nil || math.Abs(h.Until.Sub(now).Hours()-11.2) > 0.01 {
		t.Fatalf("until=%v", h.Until)
	}
	if h.WithPV <= h.Conservative {
		t.Fatal("PV must not make the estimate worse")
	}
}

func TestIntegrateForecastCapsAtFull(t *testing.T) {
	// SOC 95 %, huge PV for 1 h, then dark. Energy must not exceed full.
	prm := params()
	f := sunny(5000, 1)
	h := HoursAtSOC(Inputs{Power: fresh(95, 450, 5000), Forecast: f, Now: now}, prm)
	// usable at full = 8000; after the sunny hour we are full, then 8000/500 = 16 h -> total 17 h
	if math.Abs(h.WithPV-17) > 0.01 {
		t.Fatalf("with_pv=%v want 17", h.WithPV)
	}
}

func TestUplinkGate(t *testing.T) {
	th := params().Uplink
	cases := []struct {
		name   string
		dish   func() model.Starlink
		fc     model.Forecast
		want   model.Light
		reason string
	}{
		{"all good", goodDish, model.Forecast{}, model.Green, "drop_5m=0.005<=0.02"},
		{"obstructed -> red", func() model.Starlink { d := goodDish(); d.ObstructedNow = model.B(true); return d }, model.Forecast{}, model.Red, "currently_obstructed"},
		{"searching -> red", func() model.Starlink { d := goodDish(); d.State = "SEARCHING"; return d }, model.Forecast{}, model.Red, "state=SEARCHING"},
		{"thermal shutdown -> red", func() model.Starlink { d := goodDish(); d.Alerts = []string{"thermal_shutdown"}; return d }, model.Forecast{}, model.Red, "thermal_shutdown"},
		{"drop 3 % -> amber", func() model.Starlink { d := goodDish(); d.Drop5m = model.F(0.03); return d }, model.Forecast{}, model.Amber, "drop_5m=0.03>0.02"},
		{"drop 8 % -> red", func() model.Starlink { d := goodDish(); d.Drop5m = model.F(0.08); return d }, model.Forecast{}, model.Red, "drop_5m=0.08>0.05"},
		{"p90 200 ms -> amber", func() model.Starlink { d := goodDish(); d.LatP90Ms = model.F(200); return d }, model.Forecast{}, model.Amber, "lat_p90=200ms>150ms"},
		{"jitter 60 ms -> red", func() model.Starlink { d := goodDish(); d.JitterMs = model.F(60); return d }, model.Forecast{}, model.Red, "jitter=60ms>50ms"},
		{"one long outage -> amber", func() model.Starlink {
			d := goodDish()
			d.Outages15m = []model.Outage{{Cause: "NO_SCHEDULE", Start: now.Add(-2 * time.Minute), DurationS: 6}}
			return d
		}, model.Forecast{}, model.Amber, "outage>=5s_in_5m"},
		{"two long outages -> red", func() model.Starlink {
			d := goodDish()
			d.Outages15m = []model.Outage{
				{Cause: "NO_SATS", Start: now.Add(-1 * time.Minute), DurationS: 6},
				{Cause: "NO_SATS", Start: now.Add(-3 * time.Minute), DurationS: 9},
			}
			return d
		}, model.Forecast{}, model.Red, "outages>=5s_in_5m=2"},
		{"old outage ignored", func() model.Starlink {
			d := goodDish()
			d.Outages15m = []model.Outage{{Cause: "NO_SATS", Start: now.Add(-10 * time.Minute), DurationS: 60}}
			return d
		}, model.Forecast{}, model.Green, ""},
		{"short outage stays green", func() model.Starlink {
			d := goodDish()
			d.Outages15m = []model.Outage{{Cause: "NO_SCHEDULE", Start: now.Add(-1 * time.Minute), DurationS: 2}}
			return d
		}, model.Forecast{}, model.Green, "short_outages_5m=1"},
		{"heating caps at amber", func() model.Starlink { d := goodDish(); d.Alerts = []string{"is_heating"}; return d }, model.Forecast{}, model.Amber, "storm_risk:is_heating"},
		{"rain forecast caps at amber", goodDish, model.Forecast{Available: true, PrecipMMNext2h: 3.5}, model.Amber, "storm_risk:precip_2h=3.5mm"},
		{"light rain stays green", goodDish, model.Forecast{Available: true, PrecipMMNext2h: 1.0}, model.Green, ""},
		{"unreachable -> unknown", func() model.Starlink {
			d := goodDish()
			d.Reachable = false
			d.Error = "starlink: gRPC reflection failed: proto: not found"
			return d
		}, model.Forecast{}, Unknown, "starlink_unknown: starlink: gRPC reflection failed"},
		{"not configured -> unknown", func() model.Starlink { return model.Starlink{} }, model.Forecast{}, Unknown, "starlink_not_configured"},
		{"no history uses instant values", func() model.Starlink {
			d := goodDish()
			d.Drop5m, d.LatP90Ms, d.JitterMs = nil, nil, nil
			d.DropNow, d.LatencyNowMs = model.F(0.0), model.F(40)
			return d
		}, model.Forecast{}, model.Green, "history_unavailable_using_instant"},
		{"no samples at all -> unknown", func() model.Starlink {
			d := goodDish()
			d.Drop5m, d.LatP90Ms, d.JitterMs, d.DropNow = nil, nil, nil, nil
			return d
		}, model.Forecast{}, Unknown, "no_uplink_samples"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := UplinkGate(tc.dish(), tc.fc, th, now)
			if g.Light != tc.want {
				t.Fatalf("light=%s want %s reasons=%v", g.Light, tc.want, g.Reasons)
			}
			if tc.reason != "" && !hasReason(g.Reasons, tc.reason) {
				t.Errorf("reasons=%v want %q", g.Reasons, tc.reason)
			}
			if g.Score < 0 || g.Score > 100 {
				t.Errorf("score out of range: %d", g.Score)
			}
		})
	}
}

func TestUplinkScoreOrdering(t *testing.T) {
	th := params().Uplink
	good := UplinkGate(goodDish(), model.Forecast{}, th, now).Score
	d := goodDish()
	d.Drop5m = model.F(0.03)
	d.LatP90Ms = model.F(220)
	worse := UplinkGate(d, model.Forecast{}, th, now).Score
	if !(good > worse) {
		t.Fatalf("score good=%d should exceed degraded=%d", good, worse)
	}
	if good < 80 {
		t.Fatalf("good link should score high, got %d", good)
	}
}

func TestPowerGate(t *testing.T) {
	prm := params()
	cases := []struct {
		name   string
		power  model.Power
		dish   model.Starlink
		want   model.Light
		reason string
	}{
		{"plenty -> green", fresh(70, 450, 0), goodDish(), model.Green, "call_budget"},
		{"SOC at reserve -> red", fresh(20, 300, 0), goodDish(), model.Red, "soc=20%<=reserve+5"},
		{"SOC reserve+5 -> red", fresh(25, 300, 0), goodDish(), model.Red, "soc=25%"},
		{"SOC reserve+6 with tiny budget", fresh(26, 300, 0), goodDish(), model.Green, "usable=600Wh>=call_budget=438Wh"},
		{"inverter fault -> red", func() model.Power { p := fresh(70, 450, 0); p.Inverters[1].Mode = "Fault"; return p }(), goodDish(), model.Red, "inverter_2_mode=Fault"},
		{"not enough for the call -> amber", fresh(27, 1500, 0), goodDish(), model.Amber, "usable=700Wh<call_budget=1938Wh"},
		{"stale -> unknown", func() model.Power { p := fresh(70, 450, 0); p.Stale = true; return p }(), goodDish(), Unknown, "power_data_stale"},
		{"dish watts from telemetry", fresh(26, 300, 0), func() model.Starlink { d := goodDish(); d.PowerW = model.F(150); return d }(), model.Green, "call_budget=562Wh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Inputs{Power: tc.power, Starlink: tc.dish, Now: now}
			h := HoursAtSOC(in, prm)
			g := PowerGate(in, h, prm)
			if g.Light != tc.want {
				t.Fatalf("light=%s want %s reasons=%v", g.Light, tc.want, g.Reasons)
			}
			if tc.reason != "" && !hasReason(g.Reasons, tc.reason) {
				t.Errorf("reasons=%v want %q", g.Reasons, tc.reason)
			}
		})
	}
}

func TestShedList(t *testing.T) {
	prm := params()
	// 5000 Wh usable, net 2500 W -> 2 h; target 8 h.
	list := ShedList(prm.Shed, 5000, 2500, 8)
	if len(list) != 2 {
		t.Fatalf("want 2 items (heater then pump reaches 10 h), got %+v", list)
	}
	if list[0].Load != "water_heater" || list[1].Load != "well_pump" {
		t.Errorf("order: %+v", list)
	}
	// heater: 5000/1300 - 5000/2500 = 3.846-2 = 1.85
	if math.Abs(list[0].GainH-1.85) > 0.01 {
		t.Errorf("heater gain=%v want 1.85", list[0].GainH)
	}
	// pump: 5000/500 - 5000/1300 = 10 - 3.846 = 6.15
	if math.Abs(list[1].GainH-6.15) > 0.01 {
		t.Errorf("pump gain=%v want 6.15", list[1].GainH)
	}
	if list[0].Control != "switch.water_heater" || list[0].Priority != 1 {
		t.Errorf("metadata lost: %+v", list[0])
	}
}

func TestShedListEdges(t *testing.T) {
	prm := params()
	if l := ShedList(prm.Shed, 5000, 300, 8); len(l) != 0 {
		t.Errorf("already above target -> empty, got %+v", l)
	}
	if l := ShedList(nil, 5000, 2500, 8); len(l) != 0 {
		t.Errorf("no loads -> empty, got %+v", l)
	}
	if l := ShedList(prm.Shed, 0, 2500, 8); len(l) != 0 {
		t.Errorf("nothing usable -> empty, got %+v", l)
	}
	// Shedding everything still short of target: all loads listed.
	l := ShedList(prm.Shed, 500, 3000, 8)
	if len(l) != 3 {
		t.Errorf("want all 3 loads listed, got %d", len(l))
	}
	// Shedding past zero net -> gain is capped, not negative or NaN.
	l = ShedList([]config.ShedLoad{{Name: "x", Watts: 5000, Priority: 1}}, 1000, 400, 8)
	if len(l) != 1 || math.IsNaN(l[0].GainH) || l[0].GainH <= 0 {
		t.Errorf("gain past zero net: %+v", l)
	}
}

func TestEvaluateCombines(t *testing.T) {
	prm := params()
	cases := []struct {
		name string
		in   Inputs
		want model.Light
		exit int
	}{
		{"green", Inputs{Power: fresh(70, 450, 0), Starlink: goodDish(), Forecast: night(), Now: now}, model.Green, 0},
		{"uplink unknown -> amber", Inputs{Power: fresh(70, 450, 0), Starlink: model.Starlink{Present: true, Reachable: false, Error: "x"}, Forecast: night(), Now: now}, model.Amber, 1},
		{"power stale -> amber", Inputs{Power: func() model.Power { p := fresh(70, 450, 0); p.Stale = true; return p }(), Starlink: goodDish(), Now: now}, model.Amber, 1},
		{"power red beats uplink unknown", Inputs{Power: fresh(10, 450, 0), Starlink: model.Starlink{Present: true}, Now: now}, model.Red, 2},
		{"obstructed red", Inputs{Power: fresh(70, 450, 0), Starlink: func() model.Starlink { d := goodDish(); d.ObstructedNow = model.B(true); return d }(), Now: now}, model.Red, 2},
		{"uplink amber + power green -> amber", Inputs{Power: fresh(70, 450, 0), Starlink: func() model.Starlink { d := goodDish(); d.Drop5m = model.F(0.03); return d }(), Now: now}, model.Amber, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(tc.in, prm)
			if v.CallOK != tc.want {
				t.Fatalf("call_ok=%s want %s reasons=%v", v.CallOK, tc.want, v.CallReasons)
			}
			if ExitCode(v.CallOK) != tc.exit {
				t.Errorf("exit=%d want %d", ExitCode(v.CallOK), tc.exit)
			}
			if v.CallOK == Unknown || v.UplinkLight == "" || v.PowerLight == "" {
				t.Errorf("verdict lights: %+v", v)
			}
			if v.Shed == nil || v.CallReasons == nil {
				t.Error("slices must be non-nil for JSON")
			}
		})
	}
}

func TestEvaluateFieldsAndShedTarget(t *testing.T) {
	prm := params()
	prm.TargetHours = 20
	v := Evaluate(Inputs{Power: fresh(70, 450, 0), Starlink: goodDish(), Forecast: night(), Now: now}, prm)
	if v.HoursWorkstation != 10 || v.HoursWithPV != 10 || v.NetLoadW != 500 {
		t.Fatalf("hours/net: %+v", v)
	}
	if v.UntilReserve == nil || !v.UntilReserve.Equal(now.Add(10*time.Hour)) {
		t.Fatalf("until=%v", v.UntilReserve)
	}
	if len(v.Shed) == 0 {
		t.Fatal("with target 20 h at 10 h runtime the shed list must not be empty")
	}
	if v.UplinkScore <= 0 {
		t.Fatal("score")
	}
	for _, r := range v.CallReasons {
		if !strings.HasPrefix(r, "uplink:") && !strings.HasPrefix(r, "power:") {
			t.Errorf("reason without side prefix: %q", r)
		}
	}
}

func TestCombine(t *testing.T) {
	cases := []struct{ a, b, want model.Light }{
		{model.Green, model.Green, model.Green},
		{model.Green, model.Amber, model.Amber},
		{model.Amber, model.Green, model.Amber},
		{model.Red, model.Green, model.Red},
		{Unknown, model.Green, model.Amber},
		{model.Green, Unknown, model.Amber},
		{Unknown, Unknown, model.Amber},
		{Unknown, model.Red, model.Red},
	}
	for _, c := range cases {
		if got := Combine(c.a, c.b); got != c.want {
			t.Errorf("Combine(%s,%s)=%s want %s", c.a, c.b, got, c.want)
		}
	}
}

func TestPRLearner(t *testing.T) {
	l := NewPRLearner(4.0, 0.78)
	if l.Observe(50, 100) {
		t.Fatal("below MinGTI must be ignored")
	}
	if pr, n := l.PR(); pr != 0.78 || n != 0 {
		t.Fatalf("prior lost: %v %d", pr, n)
	}
	// 800 W/m² on 4 kWp = 3200 W expected at PR 1; measured 2400 -> 0.75
	if !l.Observe(800, 2400) {
		t.Fatal("sample should be used")
	}
	if pr, n := l.PR(); math.Abs(pr-0.75) > 1e-9 || n != 1 {
		t.Fatalf("first sample should set PR: %v %d", pr, n)
	}
	for i := 0; i < 200; i++ {
		l.Observe(800, 2800) // 0.875
	}
	if pr, _ := l.PR(); math.Abs(pr-0.875) > 0.01 {
		t.Fatalf("EMA should converge to 0.875, got %v", pr)
	}
	l.Observe(800, 10000) // clipped at 1.05
	if pr, _ := l.PR(); pr > 1.05 {
		t.Fatalf("PR must be clamped: %v", pr)
	}
	z := NewPRLearner(0, 0.78)
	if z.Observe(800, 2400) {
		t.Fatal("unknown array size cannot learn")
	}
}

func TestFmtNum(t *testing.T) {
	if fmtNum(0.02, "") != "0.02" || fmtNum(150, "ms") != "150ms" || fmtNum(0.005, "") != "0.005" {
		t.Fatal(fmtNum(0.02, ""), fmtNum(150, "ms"), fmtNum(0.005, ""))
	}
}
