package bus

import (
	"encoding/json"
	"strconv"

	"github.com/rafe-walker/deadzone/internal/model"
)

// Message is one MQTT publication.
type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

// Topics computes every topic name for a prefix so the daemon, the
// discovery payloads and the docs agree.
type Topics struct {
	Prefix string
}

func (t Topics) State() string        { return t.Prefix + "/state" }
func (t Topics) Verdict() string      { return t.Prefix + "/verdict" }
func (t Topics) HoursAtSOC() string   { return t.Prefix + "/hours_at_soc" }
func (t Topics) ShedList() string     { return t.Prefix + "/shed_list" }
func (t Topics) Availability() string { return t.Prefix + "/availability" }
func (t Topics) CallReasons() string  { return t.Prefix + "/verdict/call_reasons" }
func (t Topics) HoursWithPV() string  { return t.Prefix + "/verdict/hours_with_pv" }
func (t Topics) Until() string        { return t.Prefix + "/verdict/until" }
func (t Topics) SOCTrust() string     { return t.Prefix + "/power/soc_trust" }
func (t Topics) UplinkScore() string  { return t.Prefix + "/uplink/starlink/score" }
func (t Topics) PVWhNext6h() string   { return t.Prefix + "/forecast/pv_wh_next_6h" }
func (t Topics) PRLearned() string    { return t.Prefix + "/forecast/pr_learned" }

func num(v float64) []byte { return []byte(strconv.FormatFloat(v, 'f', 2, 64)) }

// StateMessages renders a State into the full set of publications.
// deadzone/state is the retained JSON contract; the rest are convenience
// scalars for automations that do not want to template JSON.
func StateMessages(prefix string, st model.State) ([]Message, error) {
	t := Topics{Prefix: prefix}
	full, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	reasons, err := json.Marshal(st.Verdict.CallReasons)
	if err != nil {
		return nil, err
	}
	shed, err := json.Marshal(st.Verdict.Shed)
	if err != nil {
		return nil, err
	}
	until := []byte("")
	if st.Verdict.UntilReserve != nil {
		until = []byte(st.Verdict.UntilReserve.UTC().Format("2006-01-02T15:04:05Z"))
	}
	return []Message{
		{Topic: t.State(), Payload: full, Retain: true},
		{Topic: t.Verdict(), Payload: []byte(st.Verdict.CallOK), Retain: true},
		{Topic: t.HoursAtSOC(), Payload: num(st.Verdict.HoursWorkstation), Retain: true},
		{Topic: t.ShedList(), Payload: shed, Retain: true},
		{Topic: t.CallReasons(), Payload: reasons, Retain: true},
		{Topic: t.HoursWithPV(), Payload: num(st.Verdict.HoursWithPV), Retain: true},
		{Topic: t.Until(), Payload: until, Retain: true},
		{Topic: t.SOCTrust(), Payload: []byte(st.Power.SOCTrust), Retain: true},
		{Topic: t.UplinkScore(), Payload: []byte(strconv.Itoa(st.Verdict.UplinkScore)), Retain: true},
		{Topic: t.PVWhNext6h(), Payload: num(st.Forecast.PVWhNext6h), Retain: true},
		{Topic: t.PRLearned(), Payload: num(st.Forecast.PRLearned), Retain: true},
	}, nil
}
