package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rafe-walker/deadzone/internal/actions"
	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/internal/source"
)

var now = time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)

type fake struct {
	name string
	snap source.Snapshot
}

func (f fake) Name() string                { return f.name }
func (f fake) Start(context.Context) error { return nil }
func (f fake) Snapshot() source.Snapshot   { return f.snap }

type prSink struct {
	gti   float64
	ok    bool
	setPR float64
	setN  int
}

func (p *prSink) SetPR(pr float64, n int) { p.setPR, p.setN = pr, n }
func (p *prSink) GTINow() (float64, bool) { return p.gti, p.ok }

func cfg() config.Config {
	c := config.Default()
	c.Forecast.Latitude, c.Forecast.Longitude = 31.9, -109.8
	c.Forecast.ArrayKWp = 4
	c.Battery.CapacityKWh = 10
	c.Shed = []config.ShedLoad{{Name: "heater", Watts: 1200, Priority: 1}}
	return c
}

func power(soc, load, pv float64) source.Snapshot {
	p := model.Power{SOCPct: model.F(soc), SOCSource: model.SOCBMS, SOCTrust: model.TrustHigh, LoadW: model.F(load), PVW: model.F(pv), LastMessage: now.Add(-time.Second)}
	return source.Snapshot{Power: &p}
}

func dish() source.Snapshot {
	s := model.Starlink{Present: true, Reachable: true, State: "CONNECTED", Drop5m: model.F(0), LatP90Ms: model.F(50), JitterMs: model.F(5), Alerts: []string{}, Outages15m: []model.Outage{}, Updated: now}
	return source.Snapshot{Starlink: &s}
}

func forecast() source.Snapshot {
	var steps []model.ForecastStep
	for i := 0; i < 96; i++ {
		steps = append(steps, model.ForecastStep{Time: now.Add(time.Duration(i) * 15 * time.Minute)})
	}
	f := model.Forecast{Available: true, Steps: steps, PRLearned: 0.78}
	return source.Snapshot{Forecast: &f}
}

func TestEvaluateFullStack(t *testing.T) {
	pr := &prSink{gti: 800, ok: true}
	a := New(cfg(), "1.0", fake{"sa", power(70, 450, 2400)}, fake{"sl", dish()}, fake{"om", forecast()}, pr, actions.NewAdvisory(true), nil)
	a.Now = func() time.Time { return now }
	st := a.Evaluate(context.Background())
	if st.Verdict.CallOK != model.Green {
		t.Fatalf("verdict=%s reasons=%v", st.Verdict.CallOK, st.Verdict.CallReasons)
	}
	if st.Uplink.Primary != "starlink" || st.Version != "1.0" || !st.TS.Equal(now) {
		t.Errorf("state header: %+v", st)
	}
	if st.Actions.Mode != "dry-run" || !st.Actions.Enabled {
		t.Errorf("actions: %+v", st.Actions)
	}
	// 2400 W measured on 800 W/m² * 4 kWp = 0.75
	if pr.setPR != 0.75 || pr.setN != 1 || st.Forecast.PRLearned != 0.75 {
		t.Errorf("PR learning not applied: sink=%+v forecast=%v", pr, st.Forecast.PRLearned)
	}
	if len(st.Forecast.Steps) != 24 {
		t.Errorf("steps should be trimmed to 6 h (24), got %d", len(st.Forecast.Steps))
	}
	if a.State().Verdict.CallOK != model.Green {
		t.Error("State() should return last evaluation")
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateMinimal(t *testing.T) {
	a := New(cfg(), "1.0", fake{"sa", power(70, 450, 0)}, nil, nil, nil, nil, nil)
	a.Now = func() time.Time { return now }
	st := a.Evaluate(context.Background())
	if st.Verdict.CallOK != model.Amber {
		t.Fatalf("no uplink -> amber, got %s (%v)", st.Verdict.CallOK, st.Verdict.CallReasons)
	}
	if st.Uplink.Primary != "none" || st.Uplink.Starlink.Present {
		t.Errorf("uplink: %+v", st.Uplink)
	}
	if st.Actions.Mode != "disabled" {
		t.Errorf("default actor should be disabled: %+v", st.Actions)
	}
	if st.Uplink.Starlink.Alerts == nil || st.Uplink.Probe.Targets == nil || st.Verdict.Shed == nil {
		t.Error("slices must be non-nil in JSON")
	}
}

func TestNoPRLearningWhenStale(t *testing.T) {
	pr := &prSink{gti: 800, ok: true}
	snap := power(70, 450, 2400)
	snap.Power.Stale = true
	a := New(cfg(), "1.0", fake{"sa", snap}, nil, nil, pr, nil, nil)
	a.Now = func() time.Time { return now }
	a.Evaluate(context.Background())
	if pr.setN != 0 {
		t.Fatal("stale power data must not train the PR")
	}
}
