package metrics

import (
	"math"
	"time"
)

type Outcome uint8

const (
	Success Outcome = iota
	HTTPError
	TechnicalError
	HTTPTechnicalError
	Canceled
	Unknown
	outcomeCount
)

type Population uint8

const (
	DefaultPopulation   Population = (1 << Success) | (1 << HTTPError) | (1 << TechnicalError) | (1 << HTTPTechnicalError)
	HTTPPopulation      Population = (1 << HTTPError) | (1 << HTTPTechnicalError)
	TechnicalPopulation Population = (1 << TechnicalError) | (1 << HTTPTechnicalError)
)

type LogicalOutcome uint8

const (
	LogicalSuccess LogicalOutcome = iota
	LogicalFailure
	LogicalDeadline
	LogicalCanceled
	LogicalUnknown
	logicalOutcomeCount
)

type Aggregate struct {
	TechnicalObserved                                                         uint64
	Initial, Retries, Started                                                 uint64
	Statuses                                                                  [1000]uint64
	NetworkFailures, AttemptTimeouts, DeadlineInterruptions, BodyReadFailures uint64
	Attempts                                                                  [outcomeCount]uint64
	Logical                                                                   [logicalOutcomeCount]uint64
	AttemptDurations                                                          [outcomeCount]Distribution
	LogicalDurations                                                          [logicalOutcomeCount]Distribution
}

type Observation struct {
	Status                                                                                   int
	NetworkFailure, AttemptTimeout, DeadlineInterruption, BodyReadFailure, Canceled, Unknown bool
	Duration                                                                                 time.Duration
}

func (a *Aggregate) Observe(o Observation) error {
	if o.Status != 0 && (o.Status < 100 || o.Status > 999) {
		return ErrInvalid
	}
	technical := o.NetworkFailure || o.AttemptTimeout || o.DeadlineInterruption || o.BodyReadFailure
	if technical && a.TechnicalObserved == math.MaxUint64 {
		return ErrOverflow
	}
	if err := a.FinishAttempt(Classify(o.Status, technical, o.Canceled, o.Unknown), o.Duration); err != nil {
		return err
	}
	if technical {
		a.TechnicalObserved++
	}
	if o.Status != 0 {
		a.Statuses[o.Status]++
	}
	if o.NetworkFailure {
		a.NetworkFailures++
	}
	if o.AttemptTimeout {
		a.AttemptTimeouts++
	}
	if o.DeadlineInterruption {
		a.DeadlineInterruptions++
	}
	if o.BodyReadFailure {
		a.BodyReadFailures++
	}
	return nil
}

func Classify(status int, technical, canceled, unknown bool) Outcome {
	if unknown {
		return Unknown
	}
	if canceled {
		return Canceled
	}
	httpError := status != 0 && (status < 200 || status > 299)
	if technical || status == 0 {
		if httpError {
			return HTTPTechnicalError
		}
		return TechnicalError
	}
	if httpError {
		return HTTPError
	}
	return Success
}

func (a *Aggregate) Admit(retry bool) error {
	if math.MaxUint64-a.Initial <= a.Retries {
		return ErrOverflow
	}
	if retry {
		a.Retries++
	} else {
		a.Initial++
	}
	return nil
}

func (a *Aggregate) FinishAttempt(o Outcome, d time.Duration) error {
	if o >= outcomeCount || d < 0 {
		return ErrInvalid
	}
	var done uint64
	for _, n := range a.Attempts {
		done += n
	}
	if done >= a.Initial+a.Retries {
		return ErrInvalid
	}
	if o < Canceled {
		if err := a.AttemptDurations[o].Add(d); err != nil {
			return err
		}
	}
	a.Attempts[o]++
	return nil
}

func (a *Aggregate) StartLogical() error {
	if a.Started == math.MaxUint64 {
		return ErrOverflow
	}
	a.Started++
	return nil
}

func (a *Aggregate) FinishLogical(o LogicalOutcome, d time.Duration) error {
	if o >= logicalOutcomeCount || d < 0 {
		return ErrInvalid
	}
	var done uint64
	for _, n := range a.Logical {
		done += n
	}
	if done >= a.Started {
		return ErrInvalid
	}
	if o < LogicalCanceled {
		if err := a.LogicalDurations[o].Add(d); err != nil {
			return err
		}
	}
	a.Logical[o]++
	return nil
}

func (a *Aggregate) Durations(p Population) (Distribution, error) {
	var d Distribution
	if p & ^Population((1<<outcomeCount)-1) != 0 {
		return d, ErrInvalid
	}
	for i := Outcome(0); i < outcomeCount; i++ {
		if p&(1<<i) != 0 {
			if err := d.Merge(&a.AttemptDurations[i]); err != nil {
				return Distribution{}, err
			}
		}
	}
	return d, nil
}
