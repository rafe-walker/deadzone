package model

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSanitizeNullsNaNAndInf(t *testing.T) {
	nan := math.NaN()
	st := State{}
	st.Power.LoadW = &nan
	st.Verdict.NetLoadW = math.Inf(1)
	st.Uplink.Starlink.LatP90Ms = F(math.Inf(-1))
	hits := Sanitize(&st)
	if len(hits) != 3 {
		t.Fatalf("hits=%v want 3", hits)
	}
	if st.Power.LoadW != nil || st.Verdict.NetLoadW != 0 || st.Uplink.Starlink.LatP90Ms != nil {
		t.Fatalf("not sanitized: %+v", st)
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatalf("still unmarshalable: %v", err)
	}
	want := map[string]bool{"power.load_w": true, "verdict.net_load_w": true, "uplink.starlink.lat_ms_p90": true}
	for _, h := range hits {
		if !want[h] {
			t.Errorf("unexpected path %q", h)
		}
	}
}
