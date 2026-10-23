package report

import (
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"time"
)

type Options struct {
	ExpectedSequence                 *uint64
	Population                       string
	TemplateID                       string
	FromIndex                        int64
	Limit, SampleOffset, SampleLimit int
}
type Latency struct {
	Count             string   `json:"count"`
	MinMS             *float64 `json:"minMs"`
	MeanMS            *float64 `json:"meanMs"`
	MaxMS             *float64 `json:"maxMs"`
	P50MS             *float64 `json:"p50Ms"`
	P90MS             *float64 `json:"p90Ms"`
	P95MS             *float64 `json:"p95Ms"`
	P99MS             *float64 `json:"p99Ms"`
	ResolutionLimited string   `json:"resolutionLimited"`
}
type CPUStats struct {
	Missing     string   `json:"missing"`
	Count       string   `json:"count"`
	MinPercent  *float64 `json:"minPercent"`
	MeanPercent *float64 `json:"meanPercent"`
	MaxPercent  *float64 `json:"maxPercent"`
}
type Summary struct {
	MeasurementSeconds *float64          `json:"measurementSeconds"`
	InitialRate        *float64          `json:"initialRate"`
	RetryRate          *float64          `json:"retryRate"`
	TotalRate          *float64          `json:"totalRate"`
	CPU                CPUStats          `json:"cpu"`
	Initial            string            `json:"initial"`
	Retries            string            `json:"retries"`
	Attempts           string            `json:"attempts"`
	LogicalStarted     string            `json:"logicalStarted"`
	LogicalSuccess     string            `json:"logicalSuccess"`
	LogicalFailure     string            `json:"logicalFailure"`
	LogicalDeadline    string            `json:"logicalDeadline"`
	LogicalCanceled    string            `json:"logicalCanceled"`
	LogicalUnknown     string            `json:"logicalUnknown"`
	HTTPErrors         string            `json:"httpErrors"`
	TechnicalErrors    string            `json:"technicalErrors"`
	FailedAttempts     string            `json:"failedAttempts"`
	CanceledAttempts   string            `json:"canceledAttempts"`
	UnknownAttempts    string            `json:"unknownAttempts"`
	SchedulerMisses    string            `json:"schedulerMisses"`
	CapacityMisses     string            `json:"capacityMisses"`
	Desired            string            `json:"desired"`
	Statuses           map[string]string `json:"statuses"`
	AttemptLatency     Latency           `json:"attemptLatency"`
	LogicalLatency     Latency           `json:"logicalLatency"`
}
type DimensionSummary struct {
	Kind       string  `json:"kind"`
	TemplateID string  `json:"templateId,omitempty"`
	Stage      int     `json:"stage"`
	Label      string  `json:"label"`
	Summary    Summary `json:"summary"`
}
type ChartPoint struct {
	CompletedCount   string   `json:"completedCount"`
	MeasuredFinished string   `json:"measuredFinished"`
	CanceledCount    string   `json:"canceledCount"`
	UnknownCount     string   `json:"unknownCount"`
	CPUReason        string   `json:"cpuReason"`
	IntervalKnown    bool     `json:"intervalKnown"`
	BasePresent      bool     `json:"basePresent"`
	CompletedRate    *float64 `json:"completedRate"`
	TargetUsers      *string  `json:"targetUsers"`
	ActiveUsers      *string  `json:"activeUsers"`
	RetiringUsers    *string  `json:"retiringUsers"`
	Index            string   `json:"index"`
	StartMS          float64  `json:"startMs"`
	EndMS            float64  `json:"endMs"`
	DesiredRate      *float64 `json:"desiredRate"`
	InitialRate      *float64 `json:"initialRate"`
	RetryRate        *float64 `json:"retryRate"`
	TotalRate        *float64 `json:"totalRate"`
	Shortfall        *float64 `json:"shortfall"`
	ErrorCount       string   `json:"errorCount"`
	P50MS            *float64 `json:"p50Ms"`
	P90MS            *float64 `json:"p90Ms"`
	P95MS            *float64 `json:"p95Ms"`
	P99MS            *float64 `json:"p99Ms"`
	CPU              *float64 `json:"cpu"`
}
type RangeInfo struct {
	Selection              string  `json:"selection,omitempty"`
	VolatileAdditionalKeys string  `json:"volatileAdditionalKeys"`
	FromIndex              string  `json:"fromIndex"`
	NextIndex              *string `json:"nextIndex"`
	TotalBuckets           string  `json:"totalBuckets"`
	Downsampled            bool    `json:"downsampled"`
	TotalSamples           string  `json:"totalSamples"`
	NextSampleOffset       *int    `json:"nextSampleOffset"`
}
type RunReport struct {
	Revision              string                  `json:"revision"`
	DurableRevision       string                  `json:"durableRevision"`
	LogicalPopulationNote string                  `json:"logicalPopulationNote,omitempty"`
	ChartScopes           map[string][]ChartPoint `json:"chartScopes,omitempty"`
	SchemaVersion         int                     `json:"schemaVersion"`
	ID                    string                  `json:"id"`
	Snapshot              domain.RunSnapshot      `json:"snapshot"`
	State                 string                  `json:"state"`
	Reason                string                  `json:"reason"`
	Cut                   time.Time               `json:"cut"`
	DurableCut            *time.Time              `json:"durableCut"`
	Durable               bool                    `json:"durable"`
	Recovery              *history.Recovery       `json:"recovery"`
	Summary               Summary                 `json:"summary"`
	Templates             []DimensionSummary      `json:"templates"`
	Stages                []DimensionSummary      `json:"stages"`
	Points                []ChartPoint            `json:"points"`
	Samples               []engine.Sample         `json:"samples"`
	Range                 RangeInfo               `json:"range"`
	Population            string                  `json:"population"`
	TemplateID            string                  `json:"templateId,omitempty"`
}
type Difference struct {
	Path  string `json:"path"`
	Left  any    `json:"left"`
	Right any    `json:"right"`
}
type Delta struct {
	Key      string   `json:"key"`
	Before   any      `json:"before"`
	After    any      `json:"after"`
	Absolute any      `json:"absolute"`
	Percent  *float64 `json:"percent"`
}
type Comparison struct {
	SchemaVersion int          `json:"schemaVersion"`
	Runs          [2]RunReport `json:"runs"`
	Differences   []Difference `json:"differences"`
	Deltas        []Delta      `json:"deltas"`
	Alignment     string       `json:"alignment"`
}
