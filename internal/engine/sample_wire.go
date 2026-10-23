package engine

import (
	"encoding/json"
	"highloader/internal/metrics"
	"time"
)

type Sample metrics.Sample
type sampleAlias Sample
type bodyWire struct {
	Data                       []byte
	Present, Truncated, Binary bool
	Captured                   int
	Total                      *uint64 `json:"Total,string"`
	ContentType                string
}
type sampleWire struct {
	*sampleAlias
	Sequence uint64        `json:"Sequence,string"`
	Attempt  uint64        `json:"Attempt,string"`
	Duration time.Duration `json:"Duration,string"`
	Request  bodyWire
	Response bodyWire
}

func (s Sample) MarshalJSON() ([]byte, error) {
	a := sampleAlias(s)
	return json.Marshal(sampleWire{sampleAlias: &a, Sequence: s.Sequence, Attempt: s.Attempt, Duration: s.Duration, Request: bodyWire(s.Request), Response: bodyWire(s.Response)})
}
func (s *Sample) UnmarshalJSON(data []byte) error {
	a := sampleAlias{}
	w := sampleWire{sampleAlias: &a}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*s = Sample(a)
	s.Sequence = w.Sequence
	s.Attempt = w.Attempt
	s.Duration = w.Duration
	s.Request = metrics.Body(w.Request)
	s.Response = metrics.Body(w.Response)
	return nil
}
