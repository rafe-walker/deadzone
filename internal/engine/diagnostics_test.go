package engine

import (
	"testing"

	"github.com/rafe-walker/deadzone/internal/model"
)

func TestUplinkGateDisablementCodeRed(t *testing.T) {
	th := params().Uplink
	for _, code := range []string{"NO_ACTIVE_ACCOUNT", "DATA_OVERAGE_SANDBOX_POLICY", "TOO_FAR_FROM_SERVICE_ADDRESS", "ROAM_RESTRICTED", "ACCOUNT_DISABLED"} {
		d := goodDish()
		d.DisablementCode = code
		g := UplinkGate(d, model.Forecast{}, th, now)
		if g.Light != model.Red || g.Score != 0 {
			t.Errorf("%s: light=%s score=%d want red/0", code, g.Light, g.Score)
		}
		if !hasReason(g.Reasons, "disablement_code="+code) {
			t.Errorf("%s: reasons=%v", code, g.Reasons)
		}
	}
}

func TestUplinkGateDisablementCodeBenign(t *testing.T) {
	th := params().Uplink
	for _, code := range []string{"", "OKAY", "UNKNOWN", "UNKNOWN_STATE"} {
		d := goodDish()
		d.DisablementCode = code
		g := UplinkGate(d, model.Forecast{}, th, now)
		if g.Light != model.Green {
			t.Errorf("%q: light=%s want green reasons=%v", code, g.Light, g.Reasons)
		}
		if hasReason(g.Reasons, "disablement_code") {
			t.Errorf("%q: benign code must not appear in reasons: %v", code, g.Reasons)
		}
	}
}

func TestUplinkGateStowedRed(t *testing.T) {
	d := goodDish()
	d.Stowed = model.B(true)
	g := UplinkGate(d, model.Forecast{}, params().Uplink, now)
	if g.Light != model.Red || !hasReason(g.Reasons, "stowed") {
		t.Fatalf("light=%s reasons=%v", g.Light, g.Reasons)
	}
	d.Stowed = model.B(false)
	if g := UplinkGate(d, model.Forecast{}, params().Uplink, now); g.Light != model.Green {
		t.Fatalf("unstowed should be green, got %s %v", g.Light, g.Reasons)
	}
}

func TestUplinkGateOverageRateLimitedRed(t *testing.T) {
	d := goodDish()
	d.OverageRateLimited = model.B(true)
	g := UplinkGate(d, model.Forecast{}, params().Uplink, now)
	if g.Light != model.Red || !hasReason(g.Reasons, "overage_rate_limited") {
		t.Fatalf("light=%s reasons=%v", g.Light, g.Reasons)
	}
}

func TestUplinkGateRateLimitReasons(t *testing.T) {
	th := params().Uplink
	cases := []struct {
		dl, ul string
		want   model.Light
		reason string
	}{
		{"NO_LIMIT", "NO_LIMIT", model.Green, ""},
		{"UNKNOWN", "", model.Green, ""},
		{"OVERAGE_LIMIT", "NO_LIMIT", model.Red, "dl_rate_limit=OVERAGE_LIMIT"},
		{"NO_LIMIT", "OVERAGE_LIMIT", model.Red, "ul_rate_limit=OVERAGE_LIMIT"},
		{"POLICY_LIMIT", "NO_LIMIT", model.Amber, "dl_rate_limit=POLICY_LIMIT"},
		{"NO_LIMIT", "LOW_SPEED_POLICY_LIMIT", model.Amber, "ul_rate_limit=LOW_SPEED_POLICY_LIMIT"},
	}
	for _, tc := range cases {
		d := goodDish()
		d.RateLimitDL, d.RateLimitUL = tc.dl, tc.ul
		g := UplinkGate(d, model.Forecast{}, th, now)
		if g.Light != tc.want {
			t.Errorf("%s/%s: light=%s want %s (%v)", tc.dl, tc.ul, g.Light, tc.want, g.Reasons)
		}
		if tc.reason != "" && !hasReason(g.Reasons, tc.reason) {
			t.Errorf("%s/%s: reasons=%v want %q", tc.dl, tc.ul, g.Reasons, tc.reason)
		}
		if tc.reason == "" && hasReason(g.Reasons, "rate_limit") {
			t.Errorf("%s/%s: unexpected rate-limit reason %v", tc.dl, tc.ul, g.Reasons)
		}
	}
}

func TestUplinkGateSelfTestFailedAmber(t *testing.T) {
	d := goodDish()
	d.HardwareSelfTest = "FAILED"
	g := UplinkGate(d, model.Forecast{}, params().Uplink, now)
	if g.Light != model.Amber || !hasReason(g.Reasons, "hardware_self_test=FAILED") {
		t.Fatalf("light=%s reasons=%v", g.Light, g.Reasons)
	}
	d.HardwareSelfTest = "PASSED"
	if g := UplinkGate(d, model.Forecast{}, params().Uplink, now); g.Light != model.Green {
		t.Fatalf("PASSED should be green, got %s", g.Light)
	}
}

func TestUplinkGateDisabledWithoutSamplesStaysRed(t *testing.T) {
	// A disabled dish with no ping history must not degrade to "unknown".
	d := goodDish()
	d.Drop5m, d.LatP90Ms, d.JitterMs, d.DropNow = nil, nil, nil, nil
	d.DisablementCode = "NO_ACTIVE_ACCOUNT"
	g := UplinkGate(d, model.Forecast{}, params().Uplink, now)
	if g.Light != model.Red {
		t.Fatalf("light=%s want red, reasons=%v", g.Light, g.Reasons)
	}
	if Combine(g.Light, model.Green) != model.Red {
		t.Fatal("red uplink must make the verdict red")
	}
}

func TestDishWattsSourceOrder(t *testing.T) {
	cases := []struct {
		name string
		dish model.Starlink
		want float64
		src  string
	}{
		{"mean wins", model.Starlink{PowerMeanW: model.F(62.5), PowerW: model.F(80)}, 62.5, "dish_mean_5m"},
		{"newest sample when no mean", model.Starlink{PowerW: model.F(80)}, 80, "dish_now"},
		{"config when dish silent", model.Starlink{}, 50, "config"},
		{"zero power is not a reading", model.Starlink{PowerMeanW: model.F(0), PowerW: model.F(0)}, 50, "config"},
	}
	for _, tc := range cases {
		w, src := DishWatts(tc.dish, 50)
		if w != tc.want || src != tc.src {
			t.Errorf("%s: got %v/%s want %v/%s", tc.name, w, src, tc.want, tc.src)
		}
	}
}

func TestPowerGateUsesDishMeanAndNamesSource(t *testing.T) {
	prm := params()
	d := goodDish()
	d.PowerMeanW = model.F(100)
	d.PowerW = model.F(300) // a heater spike in the newest sample must not drive the budget
	in := Inputs{Power: fresh(26, 300, 0), Starlink: d, Now: now}
	h := HoursAtSOC(in, prm)
	g := PowerGate(in, h, prm)
	// (300 + 100) * 1 h * 1.25 = 500 Wh
	if !hasReason(g.Reasons, "call_budget=500Wh") || !hasReason(g.Reasons, "dish_w=100(dish_mean_5m)") {
		t.Fatalf("reasons=%v", g.Reasons)
	}
	in.Starlink = goodDish()
	g = PowerGate(in, HoursAtSOC(in, prm), prm)
	if !hasReason(g.Reasons, "dish_w=50(config)") {
		t.Fatalf("fallback reason missing: %v", g.Reasons)
	}
}

func TestEvaluateCarriesDiagnosticsReasons(t *testing.T) {
	prm := params()
	d := goodDish()
	d.Stowed = model.B(true)
	v := Evaluate(Inputs{Power: fresh(70, 450, 0), Starlink: d, Forecast: night(), Now: now}, prm)
	if v.CallOK != model.Red || v.UplinkLight != model.Red || v.PowerLight != model.Green {
		t.Fatalf("verdict %s uplink %s power %s", v.CallOK, v.UplinkLight, v.PowerLight)
	}
	if !hasReason(v.CallReasons, "uplink:stowed") || !hasReason(v.CallReasons, "power:dish_w=") {
		t.Fatalf("reasons=%v", v.CallReasons)
	}
}
