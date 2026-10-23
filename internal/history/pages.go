package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"time"
)

type ReportQuery struct {
	TemplateID       string
	FromIndex        int64
	Limit            int
	SampleOffset     int
	SampleLimit      int
	ExpectedSequence *uint64
}
type ReportPage struct {
	Limit            int    `json:"limit"`
	Scope            string `json:"scope"`
	FromIndex        int64  `json:"fromIndex,string"`
	NextIndex        *int64 `json:"nextIndex,omitempty,string"`
	TotalBuckets     int64  `json:"totalBuckets,string"`
	SampleOffset     int    `json:"sampleOffset,string"`
	NextSampleOffset *int   `json:"nextSampleOffset,omitempty,string"`
	TotalSamples     int64  `json:"totalSamples,string"`
	Sequence         uint64 `json:"sequence,string"`
}
type BucketAvailability struct {
	TemplateID    string `json:"templateId,omitempty"`
	Index         int64  `json:"index,string"`
	BasePresent   bool   `json:"basePresent"`
	IntervalKnown bool   `json:"intervalKnown"`
}

func (s *Store) ReadReportPage(ctx context.Context, id string, q ReportQuery) (Report, error) {
	var out Report
	if q.FromIndex < 0 || q.Limit < 1 || q.Limit > 2000 || q.SampleOffset < 0 || q.SampleLimit < 0 || q.SampleLimit > 2000 {
		return out, errors.New("history: invalid report page")
	}
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Record, err = decodeRecord(tx.QueryRowContext(ctx, recordSQL+" WHERE id=?", id))
	if err != nil {
		return out, err
	}
	if q.ExpectedSequence != nil && *q.ExpectedSequence != out.LatestSequence {
		return Report{}, ErrStale
	}
	out.Page = &ReportPage{Scope: q.TemplateID, FromIndex: q.FromIndex, SampleOffset: q.SampleOffset, Sequence: out.LatestSequence, Limit: q.Limit}
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT idx FROM buckets WHERE run_id=? AND template=? UNION SELECT idx FROM completion_buckets WHERE run_id=? AND template=?)", id, q.TemplateID, id, q.TemplateID).Scan(&out.Page.TotalBuckets)
	if err != nil {
		return out, err
	}
	sampleWhere := "run_id=?"
	sampleArgs := []any{id}
	if q.TemplateID != "" {
		sampleWhere += " AND template=?"
		sampleArgs = append(sampleArgs, q.TemplateID)
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM samples WHERE "+sampleWhere, sampleArgs...).Scan(&out.Page.TotalSamples); err != nil {
		return out, err
	}
	out.Buckets, out.Completions, out.BucketAvailability, err = readMergedBuckets(ctx, tx, out.Record, &q, q.Limit+1)
	if err != nil {
		return out, err
	}
	if len(out.Buckets) > q.Limit {
		next := out.Buckets[q.Limit].Index
		out.Page.NextIndex = &next
		out.Buckets = out.Buckets[:q.Limit]
		out.BucketAvailability = out.BucketAvailability[:q.Limit]
		filtered := out.Completions[:0]
		for _, d := range out.Completions {
			if d.Index < next {
				filtered = append(filtered, d)
			}
		}
		out.Completions = filtered
	}
	out.Aggregates, err = readValues[engine.Dimension](ctx, tx, "SELECT data FROM aggregates WHERE run_id=? ORDER BY kind,template,stage", id)
	if err != nil {
		return out, err
	}
	sampleArgs = append(sampleArgs, q.SampleLimit, q.SampleOffset)
	out.Samples, err = readValues[engine.Sample](ctx, tx, "SELECT data FROM samples WHERE "+sampleWhere+" ORDER BY logical,attempt LIMIT ? OFFSET ?", sampleArgs...)
	if err != nil {
		return out, err
	}
	if q.SampleLimit > 0 && int64(q.SampleOffset)+int64(len(out.Samples)) < out.Page.TotalSamples {
		next := q.SampleOffset + len(out.Samples)
		out.Page.NextSampleOffset = &next
	}
	return out, tx.Commit()
}

func readMergedBuckets(ctx context.Context, tx *sql.Tx, r Record, q *ReportQuery, limit int) ([]engine.Bucket, []engine.CompletionDelta, []BucketAvailability, error) {
	where := "run_id=?"
	args := []any{r.RunID}
	if q != nil {
		where += " AND template=? AND idx>=?"
		args = append(args, q.TemplateID, q.FromIndex)
	}
	both := append(append([]any{}, args...), args...)
	both = append(both, limit, r.RunID, r.RunID)
	query := "WITH keys AS (SELECT template,idx FROM buckets WHERE " + where + " UNION SELECT template,idx FROM completion_buckets WHERE " + where + " ORDER BY template,idx LIMIT ?) SELECT k.template,k.idx,b.data,c.data FROM keys k LEFT JOIN buckets b ON b.run_id=? AND b.template=k.template AND b.idx=k.idx LEFT JOIN completion_buckets c ON c.run_id=? AND c.template=k.template AND c.idx=k.idx ORDER BY k.template,k.idx"
	rows, err := tx.QueryContext(ctx, query, both...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	buckets := []engine.Bucket{}
	completions := []engine.CompletionDelta{}
	availability := []BucketAvailability{}
	for rows.Next() {
		var template string
		var index int64
		var base, completion []byte
		if err = rows.Scan(&template, &index, &base, &completion); err != nil {
			return nil, nil, nil, err
		}
		bucket, delta, available, err := projectBucket(r, template, index, base, completion)
		if err != nil {
			return nil, nil, nil, err
		}
		buckets = append(buckets, bucket)
		availability = append(availability, available)
		if delta != nil {
			completions = append(completions, *delta)
		}
	}
	return buckets, completions, availability, rows.Err()
}

func projectBucket(r Record, template string, index int64, base, completion []byte) (engine.Bucket, *engine.CompletionDelta, BucketAvailability, error) {
	b := engine.Bucket{TemplateID: template, Index: index}
	a := BucketAvailability{TemplateID: template, Index: index, BasePresent: len(base) > 0}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &b); err != nil {
			return b, nil, a, err
		}
		a.IntervalKnown = b.EndNS > b.StartNS
	} else {
		b.StartNS = index * int64(time.Second)
		b.EndNS = b.StartNS
		b.CPUReason = "base interval unavailable"
		if r.Checkpoint != nil && r.Checkpoint.ElapsedNS > b.StartNS {
			end := b.StartNS + int64(time.Second)
			if end < b.StartNS || end > r.Checkpoint.ElapsedNS {
				end = r.Checkpoint.ElapsedNS
			}
			b.EndNS = end
			a.IntervalKnown = true
		}
	}
	if len(completion) == 0 {
		return b, nil, a, nil
	}
	d := &engine.CompletionDelta{TemplateID: template, Index: index}
	if err := json.Unmarshal(completion, &d.Value); err != nil {
		return b, nil, a, err
	}
	if err := d.Value.Validate(); err != nil {
		return b, nil, a, err
	}
	b.Errors = uint64(d.Value.Attempts[1]) + uint64(d.Value.Attempts[2]) + uint64(d.Value.Attempts[3])
	var err error
	b.Durations, err = d.Value.AttemptDistribution(metrics.DefaultPopulation)
	return b, d, a, err
}
