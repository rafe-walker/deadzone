package starlink

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

// node is a loosely typed JSON object that accepts both proto original
// (snake_case) and lowerCamelCase field names, so fixtures captured with
// grpcurl (camelCase by default) and our own OrigName output both parse.
type node map[string]any

func (n node) lookup(key string) (any, bool) {
	if v, ok := n[key]; ok {
		return v, true
	}
	if v, ok := n[camel(key)]; ok {
		return v, true
	}
	return nil, false
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func (n node) obj(key string) node {
	v, ok := n.lookup(key)
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return node(m)
}

func (n node) num(key string) *float64 {
	v, ok := n.lookup(key)
	if !ok {
		return nil
	}
	return toFloat(v)
}

func toFloat(v any) *float64 {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return &x
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return nil
		}
		return &f
	case string:
		// protobuf JSON encodes non-finite floats as the strings "NaN",
		// "Infinity" and "-Infinity"; ParseFloat accepts all three.
		f, err := strconv.ParseFloat(x, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return &f
	case bool:
		f := 0.0
		if x {
			f = 1
		}
		return &f
	}
	return nil
}

func (n node) boolean(key string) *bool {
	v, ok := n.lookup(key)
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

func (n node) str(key string) string {
	v, ok := n.lookup(key)
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func (n node) floats(key string) []float64 {
	v, ok := n.lookup(key)
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, e := range arr {
		f := toFloat(e)
		if f == nil {
			out = append(out, math.NaN())
			continue
		}
		out = append(out, *f)
	}
	return out
}

func (n node) list(key string) []node {
	v, ok := n.lookup(key)
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]node, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, node(m))
		}
	}
	return out
}

func decode(raw []byte) (node, error) {
	var n node
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("starlink: decode response: %w", err)
	}
	return n, nil
}

// alertNames is the order alerts are reported in. Unknown alert keys that
// are true are appended after these.
var alertNames = []string{
	"motors_stuck", "thermal_throttle", "thermal_shutdown", "mast_not_near_vertical",
	"unexpected_location", "slow_ethernet_speeds", "slow_ethernet_speeds_100",
	"roaming", "install_pending", "is_heating", "power_supply_thermal_throttle",
	"is_power_save_idle", "moving_while_not_allowed", "moving_too_fast_for_policy",
	"dbf_telem_stale", "lower_signal_than_predicted", "obstruction_map_reset",
	"dish_water_detected", "router_water_detected",
}

// ApplyStatus fills the status-derived fields of s from a get_status
// response (the dish_get_status message). History-derived fields are left
// untouched so the two pollers can run at different rates.
func ApplyStatus(s *model.Starlink, raw []byte, now time.Time) error {
	root, err := decode(raw)
	if err != nil {
		return err
	}
	st := root.obj("dish_get_status")
	if st == nil {
		return fmt.Errorf("starlink: response has no dish_get_status (keys: %s)", keys(root))
	}
	s.Present = true
	s.Reachable = true
	s.Error = ""
	s.Updated = now
	s.State = st.str("state")
	if di := st.obj("device_info"); di != nil {
		s.HardwareVersion = di.str("hardware_version")
		s.SoftwareVersion = di.str("software_version")
	}
	if ds := st.obj("device_state"); ds != nil {
		s.UptimeS = ds.num("uptime_s")
	}
	s.DropNow = st.num("pop_ping_drop_rate")
	s.LatencyNowMs = st.num("pop_ping_latency_ms")
	s.DownlinkBps = st.num("downlink_throughput_bps")
	s.UplinkBps = st.num("uplink_throughput_bps")
	s.SNRAboveNoiseFloor = st.boolean("is_snr_above_noise_floor")
	// Current firmware carries the disablement code and rate-limit reasons
	// in get_status (fields 1024, 1044, 1045); older firmware omits them
	// and get_diagnostics fills disablement_code instead.
	if dc := st.str("disablement_code"); dc != "" {
		s.DisablementCode = dc
	}
	s.RateLimitDL = st.str("dl_bandwidth_restricted_reason")
	s.RateLimitUL = st.str("ul_bandwidth_restricted_reason")
	if os := st.obj("obstruction_stats"); os != nil {
		s.ObstructedNow = os.boolean("currently_obstructed")
		s.FractionObstructed = os.num("fraction_obstructed")
		s.ObstructionDurS = os.num("avg_prolonged_obstruction_duration_s")
		s.ObstructionIntvlS = os.num("avg_prolonged_obstruction_interval_s")
	}
	s.Alerts = []string{}
	if al := st.obj("alerts"); al != nil {
		seen := map[string]bool{}
		for _, name := range alertNames {
			if b := al.boolean(name); b != nil && *b {
				s.Alerts = append(s.Alerts, name)
				seen[name] = true
			}
		}
		var extra []string
		for k, v := range al {
			if b, ok := v.(bool); ok && b && !seen[snake(k)] {
				extra = append(extra, snake(k))
			}
		}
		sort.Strings(extra)
		s.Alerts = append(s.Alerts, extra...)
	}
	return nil
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func keys(n node) string {
	var ks []string
	for k := range n {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

// ApplyDiagnostics fills the diagnostics-derived fields from a
// get_diagnostics response (dish_get_diagnostics). The message is small and
// changes rarely, so the source polls it once a minute. Unlike
// get_status it is not available on every firmware; the caller decides
// what to do when the dish rejects the request.
func ApplyDiagnostics(s *model.Starlink, raw []byte, now time.Time) error {
	root, err := decode(raw)
	if err != nil {
		return err
	}
	d := root.obj("dish_get_diagnostics")
	if d == nil {
		return fmt.Errorf("starlink: response has no dish_get_diagnostics (keys: %s)", keys(root))
	}
	s.DiagnosticsUpdated = now
	s.DiagnosticsError = ""
	if dc := d.str("disablement_code"); dc != "" {
		s.DisablementCode = dc
	}
	s.Stowed = d.boolean("stowed")
	s.OverageRateLimited = d.boolean("overage_rate_limited")
	s.HardwareSelfTest = d.str("hardware_self_test")
	if loc := d.obj("location"); loc != nil {
		s.LocationEnabled = loc.boolean("enabled")
	} else {
		s.LocationEnabled = nil
	}
	if s.HardwareVersion == "" {
		s.HardwareVersion = d.str("hardware_version")
	}
	if s.SoftwareVersion == "" {
		s.SoftwareVersion = d.str("software_version")
	}
	return nil
}

// HistoryWindow is how many 1 s samples the call gate looks at (5 min).
const HistoryWindow = 300

// HistoryLen is the dish's full ring (15 min at 1 Hz) and the span of the
// status-page sparkline.
const HistoryLen = 900

// HistoryBuckets is how many points the sparkline gets (5 s each).
const HistoryBuckets = 180

// ApplyHistory fills the history-derived fields (drop_5m, latency
// percentiles, jitter, outages, power) from a get_history response.
func ApplyHistory(s *model.Starlink, raw []byte, now time.Time) error {
	_, err := applyHistory(s, raw, now)
	return err
}

func applyHistory(s *model.Starlink, raw []byte, now time.Time) (model.History, error) {
	root, err := decode(raw)
	if err != nil {
		return model.History{}, err
	}
	h := root.obj("dish_get_history")
	if h == nil {
		return model.History{}, fmt.Errorf("starlink: response has no dish_get_history (keys: %s)", keys(root))
	}
	s.Present = true
	s.Reachable = true
	s.Updated = now
	s.HistoryUpdated = now

	current := int64(0)
	if c := h.num("current"); c != nil {
		current = int64(*c)
	}
	drops := ordered(h.floats("pop_ping_drop_rate"), current)
	lats := ordered(h.floats("pop_ping_latency_ms"), current)
	power := ordered(h.floats("power_in"), current)
	hist := Downsample(tail(drops, HistoryLen), tail(lats, HistoryLen), now, HistoryBuckets)

	drops = tail(drops, HistoryWindow)
	lats = tail(lats, HistoryWindow)
	power = tail(power, HistoryWindow)
	s.HistorySamples = len(drops)

	if len(drops) > 0 {
		sum := 0.0
		n := 0
		for _, d := range drops {
			if math.IsNaN(d) {
				continue
			}
			sum += d
			n++
		}
		if n > 0 {
			s.Drop5m = model.F(sum / float64(n))
		}
	}
	// Latency samples during a full drop are meaningless (reported as 0).
	var valid []float64
	var jitterSum float64
	var jitterN int
	var prev float64
	hasPrev := false
	for i, l := range lats {
		dropped := i < len(drops) && drops[i] >= 1
		if math.IsNaN(l) || l <= 0 || dropped {
			hasPrev = false
			continue
		}
		valid = append(valid, l)
		if hasPrev {
			jitterSum += math.Abs(l - prev)
			jitterN++
		}
		prev = l
		hasPrev = true
	}
	if len(valid) > 0 {
		sorted := append([]float64(nil), valid...)
		sort.Float64s(sorted)
		s.LatP50Ms = model.F(percentile(sorted, 0.50))
		s.LatP90Ms = model.F(percentile(sorted, 0.90))
	}
	if jitterN > 0 {
		s.JitterMs = model.F(jitterSum / float64(jitterN))
	}
	// Power: the dish only reports it here (power_in, W per 1-s sample);
	// get_status has no power field. Newest sample for display, the window
	// mean for the call budget (a dish heater or snow-melt cycle shows up
	// in the mean long before it shows in one sample).
	s.PowerW, s.PowerMeanW, s.PowerMinW, s.PowerMaxW = nil, nil, nil, nil
	if len(power) > 0 {
		if p := power[len(power)-1]; !math.IsNaN(p) && p > 0 {
			s.PowerW = model.F(p)
		}
		sum, n := 0.0, 0
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, p := range power {
			if math.IsNaN(p) || p <= 0 {
				continue
			}
			sum += p
			n++
			lo = math.Min(lo, p)
			hi = math.Max(hi, p)
		}
		if n > 0 {
			s.PowerMeanW = model.F(math.Round(sum/float64(n)*10) / 10)
			s.PowerMinW = model.F(lo)
			s.PowerMaxW = model.F(hi)
		}
	}

	s.Outages15m = []model.Outage{}
	cutoff := now.Add(-15 * time.Minute)
	for _, o := range h.list("outages") {
		out := model.Outage{Cause: o.str("cause")}
		if d := o.num("duration_ns"); d != nil {
			out.DurationS = *d / 1e9
		}
		if b := o.boolean("did_switch"); b != nil {
			out.DidSwitch = *b
		}
		if ts := o.num("start_timestamp_ns"); ts != nil && *ts > 1e17 {
			// Absolute (ns since the Unix epoch). Values below 1e17 would
			// be a relative/monotonic clock; those keep a zero Start and
			// are treated as recent, because the buffer only spans 15 min.
			out.Start = time.Unix(0, int64(*ts)).UTC()
			if out.Start.Before(cutoff) {
				continue
			}
		}
		s.Outages15m = append(s.Outages15m, out)
	}
	return hist, nil
}

// Downsample buckets 1-s drop/latency samples (oldest first, newest last)
// into at most buckets points for the sparkline. Per bucket: mean drop
// rate and nearest-rank p90 of the valid latencies (samples during a full
// drop are 0 and skipped). The last bucket ends at `at`.
func Downsample(drops, lats []float64, at time.Time, buckets int) model.History {
	n := len(drops)
	if len(lats) > n {
		n = len(lats)
	}
	h := model.History{At: at, StepS: 1, Drop: []*float64{}, LatMs: []*float64{}}
	if n == 0 || buckets <= 0 {
		return h
	}
	per := 1
	if n > buckets {
		per = int(math.Ceil(float64(n) / float64(buckets)))
	}
	h.StepS = float64(per)
	// Align so the newest bucket is full: walk from the end.
	for end := n; end > 0; end -= per {
		start := end - per
		if start < 0 {
			start = 0
		}
		sum, cnt := 0.0, 0
		var valid []float64
		for i := start; i < end; i++ {
			dropped := false
			if i < len(drops) && !math.IsNaN(drops[i]) {
				sum += drops[i]
				cnt++
				dropped = drops[i] >= 1
			}
			if i < len(lats) && !math.IsNaN(lats[i]) && lats[i] > 0 && !dropped {
				valid = append(valid, lats[i])
			}
		}
		var d, l *float64
		if cnt > 0 {
			d = model.F(math.Round(sum/float64(cnt)*1000) / 1000)
		}
		if len(valid) > 0 {
			sort.Float64s(valid)
			l = model.F(math.Round(percentile(valid, 0.90)*10) / 10)
		}
		h.Drop = append([]*float64{d}, h.Drop...)
		h.LatMs = append([]*float64{l}, h.LatMs...)
	}
	return h
}

// ordered unrolls the dish's ring buffer so index 0 is the oldest sample
// and the last element is the newest. `current` is the dish's running
// sample counter; the newest sample sits at (current-1) mod len.
func ordered(ring []float64, current int64) []float64 {
	n := int64(len(ring))
	if n == 0 {
		return nil
	}
	if current <= 0 || current >= n {
		if current <= 0 {
			return append([]float64(nil), ring...)
		}
		start := current % n
		out := make([]float64, 0, n)
		out = append(out, ring[start:]...)
		out = append(out, ring[:start]...)
		return out
	}
	return append([]float64(nil), ring[:current]...)
}

func tail(v []float64, n int) []float64 {
	if len(v) <= n {
		return v
	}
	return v[len(v)-n:]
}

// percentile uses nearest-rank on a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
