package metrics

import (
	"maps"
	"slices"
	"time"
	"unicode/utf8"
)

type Body struct {
	Data                       []byte
	Present, Truncated, Binary bool
	Captured                   int
	Total                      *uint64
	ContentType                string
}
type Sample struct {
	RunID, TemplateID, LogicalID, UserID string
	Sequence                             uint64
	Attempt                              uint64
	Started, Ended                       time.Time
	Duration                             time.Duration
	Method, URL                          string
	RequestHeaders, ResponseHeaders      map[string][]string
	Request, Response                    Body
	Status                               *int
	Outcome                              Outcome
	RetryContext                         string
}
type Samples struct {
	successQuota, failureQuota, bodyCap int
	counts                              map[string][2]int
	values                              []Sample
}

func NewSamples(successQuota, failureQuota, bodyCap int) (*Samples, error) {
	if successQuota < 0 || failureQuota < 0 || bodyCap < 0 {
		return nil, ErrInvalid
	}
	return &Samples{successQuota: successQuota, failureQuota: failureQuota, bodyCap: bodyCap, counts: make(map[string][2]int)}, nil
}

func CaptureBody(body Body, cap int) Body {
	if cap < 0 {
		cap = 0
	}
	n := len(body.Data)
	if n > cap {
		n = cap
	}
	body.Truncated = body.Truncated || len(body.Data) > n
	body.Binary = body.Binary || !utf8.Valid(body.Data)
	body.Data = slices.Clone(body.Data[:n])
	body.Captured = n
	if body.Total != nil {
		x := *body.Total
		body.Total = &x
		if x > uint64(n) {
			body.Truncated = true
		}
	}
	return body
}

func cloneSample(s Sample, cap int) Sample {
	s.Request = CaptureBody(s.Request, cap)
	s.Response = CaptureBody(s.Response, cap)
	s.RequestHeaders = maps.Clone(s.RequestHeaders)
	for k, v := range s.RequestHeaders {
		s.RequestHeaders[k] = slices.Clone(v)
	}
	s.ResponseHeaders = maps.Clone(s.ResponseHeaders)
	for k, v := range s.ResponseHeaders {
		s.ResponseHeaders[k] = slices.Clone(v)
	}
	if s.Status != nil {
		x := *s.Status
		s.Status = &x
	}
	return s
}

func (s *Samples) Retain(v Sample) bool {
	if v.Outcome >= Unknown {
		return false
	}
	category, limit := 0, s.successQuota
	if v.Outcome != Success {
		category, limit = 1, s.failureQuota
	}
	counts := s.counts[v.TemplateID]
	if counts[category] >= limit {
		return false
	}
	v = cloneSample(v, s.bodyCap)
	counts[category]++
	s.counts[v.TemplateID] = counts
	s.values = append(s.values, v)
	return true
}

func (s *Samples) All() []Sample {
	out := make([]Sample, len(s.values))
	for i, v := range s.values {
		out[i] = cloneSample(v, s.bodyCap)
	}
	return out
}
