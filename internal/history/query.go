package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"time"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const recordSQL = "SELECT id,created,state,reason,seq,snapshot,checkpoint,recovery,completion_seq,counts FROM runs"

func decodeRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var snapshot, cp, recovery, counts []byte
	e := row.Scan(&r.RunID, &r.CreatedAt, &r.State, &r.Reason, &r.LatestSequence, &snapshot, &cp, &recovery, &r.CompletionSequence, &counts)
	r.SchemaVersion = 2
	if errors.Is(e, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if len(counts) > 0 {
		if e = json.Unmarshal(counts, &r.Counts); e != nil {
			return r, e
		}
	}
	if e = json.Unmarshal(snapshot, &r.Snapshot); e != nil {
		return r, e
	}
	if len(cp) > 0 {
		if e = json.Unmarshal(cp, &r.Checkpoint); e != nil {
			return r, e
		}
		cut := r.Checkpoint.Cut
		r.DurableCut = &cut
	}
	if len(recovery) > 0 {
		if e = json.Unmarshal(recovery, &r.Recovery); e != nil {
			return r, e
		}
	}
	r.URL = r.Snapshot.Draft.URL
	r.Mode = r.Snapshot.Draft.Profile.Mode
	r.TemplateCount = len(r.Snapshot.Draft.SelectedIDs)
	return r, nil
}

func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	return decodeRecord(s.reader.QueryRowContext(ctx, recordSQL+" WHERE id=?", id))
}

func (s *Store) List(ctx context.Context, limit, offset int) ([]Summary, error) {
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, errors.New("history: invalid page")
	}
	rows, e := s.reader.QueryContext(ctx, "SELECT id,created,state,reason,seq,url,mode,template_count,cut,counts FROM runs ORDER BY created DESC,id LIMIT ? OFFSET ?", limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var r Summary
		var cut sql.NullString
		var counts []byte
		if e = rows.Scan(&r.RunID, &r.CreatedAt, &r.State, &r.Reason, &r.LatestSequence, &r.URL, &r.Mode, &r.TemplateCount, &cut, &counts); e != nil {
			return nil, e
		}
		if len(counts) > 0 {
			if e = json.Unmarshal(counts, &r.Counts); e != nil {
				return nil, e
			}
		}
		if cut.Valid {
			t, err := time.Parse(time.RFC3339Nano, cut.String)
			if err != nil {
				return nil, err
			}
			r.DurableCut = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readValues[T any](ctx context.Context, q queryer, query string, args ...any) ([]T, error) {
	rows, e := q.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		var v T
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Buckets(ctx context.Context, id string, from int64, limit int) ([]engine.Bucket, error) {
	return s.BucketsForTemplate(ctx, id, "", from, limit)
}

func (s *Store) Samples(ctx context.Context, id string, limit, offset int) ([]engine.Sample, error) {
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, errors.New("history: invalid page")
	}
	return readValues[engine.Sample](ctx, s.reader, "SELECT data FROM samples WHERE run_id=? ORDER BY logical,attempt LIMIT ? OFFSET ?", id, limit, offset)
}

func (s *Store) Aggregates(ctx context.Context, id string) ([]engine.Dimension, error) {
	return readValues[engine.Dimension](ctx, s.reader, "SELECT data FROM aggregates WHERE run_id=? ORDER BY kind,template,stage", id)
}

func (s *Store) Repeat(ctx context.Context, id string) (domain.Draft, error) {
	r, e := s.Get(ctx, id)
	return r.Snapshot.Draft, e
}

func (s *Store) ReadReport(ctx context.Context, id string) (Report, error) {
	var out Report
	tx, e := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Record, e = decodeRecord(tx.QueryRowContext(ctx, recordSQL+" WHERE id=?", id))
	if e != nil {
		return out, e
	}
	out.Buckets, out.Completions, out.BucketAvailability, e = readMergedBuckets(ctx, tx, out.Record, nil, -1)
	if e != nil {
		return out, e
	}
	out.Aggregates, e = readValues[engine.Dimension](ctx, tx, "SELECT data FROM aggregates WHERE run_id=? ORDER BY kind,template,stage", id)
	if e != nil {
		return out, e
	}
	out.Samples, e = readValues[engine.Sample](ctx, tx, "SELECT data FROM samples WHERE run_id=? ORDER BY logical,attempt", id)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func (s *Store) BucketsForTemplate(ctx context.Context, id, template string, from int64, limit int) ([]engine.Bucket, error) {
	if from < 0 || limit < 1 || limit > 1000 {
		return nil, errors.New("history: invalid page")
	}
	report, err := s.ReadReportPage(ctx, id, ReportQuery{TemplateID: template, FromIndex: from, Limit: limit})
	if errors.Is(err, ErrNotFound) {
		return []engine.Bucket{}, nil
	}
	return report.Buckets, err
}
