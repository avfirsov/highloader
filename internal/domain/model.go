package domain

import "encoding/json"

type Diagnostic struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Offset  int64  `json:"offset,omitempty"`
}
type HeaderRow struct {
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Suppress bool   `json:"suppress"`
}
type FileHeader struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}
type EffectiveHeader struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Deleted bool   `json:"deleted"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
}
type TemplateSource struct {
	ID          string          `json:"id"`
	Filename    string          `json:"filename"`
	Raw         []byte          `json:"raw"`
	Digest      string          `json:"digest"`
	Body        json.RawMessage `json:"body,omitempty"`
	Headers     []FileHeader    `json:"headers"`
	Diagnostics []Diagnostic    `json:"diagnostics"`
	ImportOrder int             `json:"importOrder"`
	Valid       bool            `json:"valid"`
}
type Auth struct {
	Mode      string `json:"mode"`
	Token     string `json:"token"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	Placement string `json:"placement"`
}
type StatusRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}
type Policy struct {
	TimeoutMS  int64         `json:"timeoutMs"`
	MaxRetries int           `json:"maxRetries"`
	Statuses   []StatusRange `json:"statuses"`
	Network    bool          `json:"network"`
	Timeouts   bool          `json:"timeouts"`
	DelayMode  string        `json:"delayMode"`
	DelayMS    int64         `json:"delayMs"`
	MaxDelayMS int64         `json:"maxDelayMs"`
	Jitter     bool          `json:"jitter"`
	DeadlineMS int64         `json:"deadlineMs"`
}
type PolicyOverride struct {
	TimeoutMS  *int64         `json:"timeoutMs,omitempty"`
	MaxRetries *int           `json:"maxRetries,omitempty"`
	Statuses   *[]StatusRange `json:"statuses,omitempty"`
	Network    *bool          `json:"network,omitempty"`
	Timeouts   *bool          `json:"timeouts,omitempty"`
	DelayMode  *string        `json:"delayMode,omitempty"`
	DelayMS    *int64         `json:"delayMs,omitempty"`
	MaxDelayMS *int64         `json:"maxDelayMs,omitempty"`
	Jitter     *bool          `json:"jitter,omitempty"`
	DeadlineMS *int64         `json:"deadlineMs,omitempty"`
}
type TemplateSettings struct {
	Method  *string        `json:"method,omitempty"`
	Headers []HeaderRow    `json:"headers"`
	Policy  PolicyOverride `json:"policy"`
	Weight  float64        `json:"weight"`
}
type Step struct {
	DurationSeconds int64 `json:"durationSeconds"`
	Level           int64 `json:"level"`
}
type Profile struct {
	Mode            string `json:"mode"`
	Steps           []Step `json:"steps"`
	RetryAccounting string `json:"retryAccounting"`
}
type Samples struct {
	Success      int64 `json:"success"`
	Failure      int64 `json:"failure"`
	BodyCapBytes int64 `json:"bodyCapBytes"`
}
type Draft struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Revision      uint64                      `json:"revision,string"`
	Templates     []TemplateSource            `json:"templates"`
	SelectedIDs   []string                    `json:"selectedIds"`
	Settings      map[string]TemplateSettings `json:"settings"`
	URL           string                      `json:"url"`
	Method        string                      `json:"method"`
	Auth          Auth                        `json:"auth"`
	Headers       []HeaderRow                 `json:"headers"`
	Policy        Policy                      `json:"policy"`
	Profile       Profile                     `json:"profile"`
	SelectionMode string                      `json:"selectionMode"`
	Capacity      int64                       `json:"capacity"`
	Samples       Samples                     `json:"samples"`
}
type EffectiveRequest struct {
	TemplateID    string            `json:"templateId"`
	URL           string            `json:"url"`
	Method        string            `json:"method"`
	MethodSource  string            `json:"methodSource"`
	Headers       []EffectiveHeader `json:"headers"`
	Policy        Policy            `json:"policy"`
	PolicySources map[string]string `json:"policySources"`
	Weight        float64           `json:"weight"`
}
type ResourceEstimate struct {
	SourceBytes         int64 `json:"sourceBytes"`
	CandidateBytes      int64 `json:"candidateBytes"`
	RetainedSampleBytes int64 `json:"retainedSampleBytes"`
	InflightBodyBytes   int64 `json:"inflightBodyBytes"`
	TotalBytes          int64 `json:"totalBytes"`
	DurationMS          int64 `json:"durationMs"`
}
type RunSnapshot struct {
	SchemaVersion int                `json:"schemaVersion"`
	RunID         string             `json:"runId"`
	CreatedAt     string             `json:"createdAt"`
	AppVersion    string             `json:"appVersion"`
	Draft         Draft              `json:"draft"`
	Effective     []EffectiveRequest `json:"effective"`
	Resources     ResourceEstimate   `json:"resources"`
}
