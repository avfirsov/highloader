package engine

import (
	"context"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"io"
	"time"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}
type Random interface {
	Intn(int) int
	Float64() float64
	Int63n(int64) int64
}
type Transport interface {
	DoAttempt(context.Context, Attempt) Result
}
type CheckpointSink interface {
	Commit(context.Context, Checkpoint) error
}
type CPUValue struct {
	ID      string
	At      time.Time
	Percent *float64
	Reason  string
}
type CPU interface{ Latest() CPUValue }
type Options struct {
	Clock          Clock
	Random         Random
	TokenRandom    io.Reader
	Transport      Transport
	Sink           CheckpointSink
	CPU            CPU
	CommitTimeout  time.Duration
	ReleaseTimeout time.Duration
}
type Attempt struct {
	RunID           string
	Sequence        uint64
	Number          uint64
	UserID          *uint64
	Stage           int
	Started         time.Time
	Timeout         time.Duration
	LogicalDeadline bool
	Request         domain.EffectiveRequest
	Body            []byte
	BodyCap         int
}
type Result struct {
	Sequence, Number uint64
	Ended            time.Time
	Observation      metrics.Observation
	Sample           metrics.Sample
}
type Unfinished struct {
	OutcomeCommitted bool    `json:"outcomeCommitted"`
	Sequence         uint64  `json:"sequence,string"`
	Attempt          uint64  `json:"attempt,string"`
	TemplateID       string  `json:"templateId"`
	UserID           *uint64 `json:"userId,omitempty,string"`
	FirstStage       int     `json:"firstStage"`
	AttemptStage     int     `json:"attemptStage"`
	Active           bool    `json:"active"`
}
type Dimension struct {
	Kind       string    `json:"kind"`
	TemplateID string    `json:"templateId,omitempty"`
	Stage      int       `json:"stage"`
	Aggregate  Aggregate `json:"aggregate"`
}

// Aggregate carries all populations without lossy browser integer conversion.
type Aggregate struct {
	TechnicalObserved     uint64                 `json:"technicalObserved,string"`
	Initial               uint64                 `json:"initial,string"`
	Retries               uint64                 `json:"retries,string"`
	Started               uint64                 `json:"started,string"`
	Statuses              map[string]string      `json:"statuses"`
	Attempts              []string               `json:"attempts"`
	Logical               []string               `json:"logical"`
	NetworkFailures       uint64                 `json:"networkFailures,string"`
	AttemptTimeouts       uint64                 `json:"attemptTimeouts,string"`
	DeadlineInterruptions uint64                 `json:"deadlineInterruptions,string"`
	BodyReadFailures      uint64                 `json:"bodyReadFailures,string"`
	AttemptDurations      []metrics.Distribution `json:"attemptDurations"`
	LogicalDurations      []metrics.Distribution `json:"logicalDurations"`
}
type Bucket struct {
	TargetUsers   *int64               `json:"targetUsers,omitempty,string"`
	ActiveUsers   *int64               `json:"activeUsers,omitempty,string"`
	RetiringUsers *int64               `json:"retiringUsers,omitempty,string"`
	SnapshotAt    time.Time            `json:"snapshotAt"`
	CPUAt         time.Time            `json:"cpuAt"`
	CPUIdentity   string               `json:"cpuIdentity,omitempty"`
	CPUReason     string               `json:"cpuReason,omitempty"`
	TemplateID    string               `json:"templateId,omitempty"`
	Index         int64                `json:"index,string"`
	StartNS       int64                `json:"startNs,string"`
	EndNS         int64                `json:"endNs,string"`
	Desired       uint64               `json:"desired,string"`
	Initial       uint64               `json:"initial,string"`
	Retries       uint64               `json:"retries,string"`
	Missed        uint64               `json:"missed,string"`
	Errors        uint64               `json:"errors,string"`
	Durations     metrics.Distribution `json:"durations"`
	CPU           *float64             `json:"cpu"`
}

// CPUAmendment updates only the overall interval's CPU metadata, never dispatch/user evidence.
type CPUAmendment struct {
	Index    int64     `json:"index,string"`
	At       time.Time `json:"at"`
	Identity string    `json:"identity"`
	Percent  *float64  `json:"percent"`
	Reason   string    `json:"reason,omitempty"`
}
type Checkpoint struct {
	CPUAmendments       []CPUAmendment    `json:"cpuAmendments,omitempty"`
	StartedAt           time.Time         `json:"startedAt"`
	ElapsedNS           int64             `json:"elapsedNs,string"`
	UnflushedCompletion []CompletionDelta `json:"unflushedCompletion,omitempty"`
	CompletionBatches   []CompletionBatch `json:"completionBatches"`
	CompletionThrough   uint64            `json:"completionThrough,string"`
	CPUStats            CPUSummary        `json:"cpuStats"`
	RunID               string            `json:"runId"`
	Sequence            uint64            `json:"sequence,string"`
	Cut                 time.Time         `json:"cut"`
	State               string            `json:"state"`
	Reason              string            `json:"reason"`
	Final               bool              `json:"final"`
	Aggregates          []Dimension       `json:"aggregates"`
	Buckets             []Bucket          `json:"buckets"`
	Samples             []Sample          `json:"samples"`
	Unfinished          []Unfinished      `json:"unfinished"`
	Desired             uint64            `json:"desired,string"`
	SchedulerMisses     uint64            `json:"schedulerMisses,string"`
	CapacityMisses      uint64            `json:"capacityMisses,string"`
}
type View struct {
	Mode             string          `json:"mode"`
	RetryAccounting  string          `json:"retryAccounting"`
	CompletedRate    *float64        `json:"completedRate"`
	TargetUsers      *int64          `json:"targetUsers,omitempty,string"`
	CPUStats         CPUSummary      `json:"cpuStats"`
	WriterReleased   bool            `json:"writerReleased"`
	Revision         uint64          `json:"revision,string"`
	RunID            string          `json:"runId"`
	State            string          `json:"state"`
	Reason           string          `json:"reason"`
	ElapsedMS        int64           `json:"elapsedMs"`
	RemainingMS      int64           `json:"remainingMs"`
	Stage            int             `json:"stage"`
	DesiredRate      *float64        `json:"desiredRate"`
	InitialRate      float64         `json:"initialRate"`
	RetryRate        float64         `json:"retryRate"`
	TotalRate        float64         `json:"totalRate"`
	Shortfall        *float64        `json:"shortfall"`
	Aggregate        Aggregate       `json:"aggregate"`
	Desired          uint64          `json:"desired,string"`
	SchedulerMisses  uint64          `json:"schedulerMisses,string"`
	CapacityMisses   uint64          `json:"capacityMisses,string"`
	Unfinished       int             `json:"unfinished"`
	Capacity         int             `json:"capacity"`
	ActiveUsers      int             `json:"activeUsers"`
	RetiringUsers    int             `json:"retiringUsers"`
	CPU              *float64        `json:"cpu"`
	CPUReason        string          `json:"cpuReason"`
	DurableSequence  uint64          `json:"durableSequence,string"`
	DurableCut       time.Time       `json:"durableCut"`
	PersistenceError string          `json:"persistenceError"`
	Durable          bool            `json:"durable"`
	WorkersReleased  bool            `json:"workersReleased"`
	Points           []metrics.Point `json:"points"`
}

// Completion evidence is schema-2 additive data, independent of base-bucket replacement.
type Counter uint64
type CompletionValue struct {
	Attempts         [6]Counter              `json:"attempts"`
	AttemptDurations [4]metrics.Distribution `json:"attemptDurations"`
	Logical          [5]Counter              `json:"logical"`
	LogicalDurations [3]metrics.Distribution `json:"logicalDurations"`
}
type CompletionDelta struct {
	TemplateID string          `json:"templateId,omitempty"`
	Index      int64           `json:"index,string"`
	Value      CompletionValue `json:"value"`
}
type CompletionBatch struct {
	Version  int               `json:"version"`
	Sequence uint64            `json:"sequence,string"`
	Deltas   []CompletionDelta `json:"deltas"`
}
type CPUSummary struct {
	Valid   uint64   `json:"valid,string"`
	Missing uint64   `json:"missing,string"`
	Sum     float64  `json:"sum"`
	Min     *float64 `json:"min"`
	Max     *float64 `json:"max"`
}
