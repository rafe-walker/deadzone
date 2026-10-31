package actions

import (
	"context"
	"testing"

	"github.com/rafe-walker/deadzone/internal/model"
)

func TestAdvisoryDisabledRecordsNothing(t *testing.T) {
	a := NewAdvisory(false)
	if a.Mode() != ModeDisabled {
		t.Fatal(a.Mode())
	}
	if err := a.Shed(context.Background(), []model.ShedItem{{Load: "x", W: 1}}); err != nil {
		t.Fatal(err)
	}
	if a.Last() != nil {
		t.Fatal("disabled actor must not record")
	}
}

func TestAdvisoryDryRunRecordsCopy(t *testing.T) {
	a := NewAdvisory(true)
	if a.Mode() != ModeDryRun {
		t.Fatal(a.Mode())
	}
	items := []model.ShedItem{{Load: "heater", W: 1200, GainH: 2}}
	if err := a.Shed(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	items[0].Load = "mutated"
	rec := a.Last()
	if rec == nil || len(rec.Items) != 1 || rec.Items[0].Load != "heater" || rec.At.IsZero() {
		t.Fatalf("record=%+v", rec)
	}
}
