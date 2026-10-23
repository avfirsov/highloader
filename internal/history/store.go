package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"log"
	"math"
	_ "modernc.org/sqlite"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrActive   = errors.New("history: active run")
	ErrNotFound = errors.New("history: run not found")
	ErrStale    = errors.New("history: stale checkpoint")
	ErrConflict = errors.New("history: conflicting checkpoint")
	ErrClosed   = errors.New("history: closed")
	ErrSchema   = errors.New("history: unsupported schema")
)

type Store struct {
	writer, reader *sql.DB
	mu             sync.Mutex
	closed         bool
	fault          func(string) error
}
type StartResult struct {
	RunID    string `json:"runId"`
	Existing bool   `json:"existing"`
}
type Summary struct {
	Counts         *ResultCounts `json:"counts"`
	RunID          string        `json:"runId"`
	CreatedAt      string        `json:"createdAt"`
	State          string        `json:"state"`
	Reason         string        `json:"reason"`
	URL            string        `json:"url"`
	Mode           string        `json:"mode"`
	TemplateCount  int           `json:"templateCount"`
	LatestSequence uint64        `json:"latestSequence,string"`
	DurableCut     *time.Time    `json:"durableCut"`
}
type Recovery struct {
	DetectedAt     time.Time           `json:"detectedAt"`
	UnobservedFrom time.Time           `json:"unobservedFrom"`
	Unknown        []engine.Unfinished `json:"unknown"`
}
type Record struct {
	Summary
	SchemaVersion      int                `json:"schemaVersion"`
	CompletionSequence uint64             `json:"completionSequence,string"`
	Snapshot           domain.RunSnapshot `json:"snapshot"`
	Checkpoint         *engine.Checkpoint `json:"checkpoint"`
	Recovery           *Recovery          `json:"recovery"`
}
type Report struct {
	Record
	VolatileAdditionalKeys int                      `json:"volatileAdditionalKeys"`
	Completions            []engine.CompletionDelta `json:"completions"`
	Page                   *ReportPage              `json:"page,omitempty"`
	BucketAvailability     []BucketAvailability     `json:"bucketAvailability"`
	Buckets                []engine.Bucket          `json:"buckets"`
	Aggregates             []engine.Dimension       `json:"aggregates"`
	Samples                []engine.Sample          `json:"samples"`
}

func Open(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := fileURI(absolute)
	q := u.Query()
	for _, p := range []string{"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(5000)"} {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	w, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	fail := func(e error) (*Store, error) { w.Close(); return nil, e }
	var version int
	if err = w.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version != 0 && version != 2 {
		return fail(fmt.Errorf("%w: database version %d, supported version 2; existing evidence preserved", ErrSchema, version))
	}
	var integrity string
	if err = w.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return fail(err)
	}
	if integrity != "ok" {
		return fail(fmt.Errorf("history: integrity check failed"))
	}
	if version == 0 {
		var n int
		if err = w.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&n); err != nil {
			return fail(err)
		}
		if n != 0 {
			return fail(ErrSchema)
		}
	} else {
		var migration int
		if err = w.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&migration); err != nil || migration != 2 {
			return fail(ErrSchema)
		}
		rows, checkErr := w.Query("SELECT r.id,r.idem,r.created,r.state,r.reason,r.active,r.snapshot,r.seq,r.checkpoint,r.recovery,r.url,r.mode,r.template_count,r.cut,r.completion_seq,r.scopes,r.counts,b.template,b.idx,b.seq,b.data,c.template,c.idx,c.data,a.kind,a.template,a.stage,a.seq,a.data,s.logical,s.attempt,s.seq,s.data,s.template FROM runs r LEFT JOIN buckets b ON b.run_id=r.id LEFT JOIN completion_buckets c ON c.run_id=r.id LEFT JOIN aggregates a ON a.run_id=r.id LEFT JOIN samples s ON s.run_id=r.id LIMIT 0")
		if checkErr != nil {
			return fail(ErrSchema)
		}
		rows.Close()
	}
	var journal string
	if err = w.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		return fail(err)
	}
	if journal != "wal" {
		return fail(fmt.Errorf("history: WAL unavailable"))
	}
	if version == 0 {
		if _, err = w.Exec(schema); err != nil {
			return fail(err)
		}
	}
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	r, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	r.SetMaxOpenConns(4)
	if err = r.Ping(); err != nil {
		r.Close()
		return fail(err)
	}
	log.Print("SQLite schema and integrity verified")
	return &Store{writer: w, reader: r}, nil
}

const schema = `BEGIN IMMEDIATE;
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
INSERT INTO schema_migrations VALUES(2,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
CREATE TABLE runs(id TEXT PRIMARY KEY, idem TEXT NOT NULL UNIQUE, created TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL, snapshot BLOB NOT NULL, seq TEXT NOT NULL DEFAULT '0', checkpoint BLOB, recovery BLOB, url TEXT NOT NULL, mode TEXT NOT NULL, template_count INTEGER NOT NULL, cut TEXT, completion_seq TEXT NOT NULL DEFAULT '0', scopes BLOB NOT NULL, counts BLOB);
CREATE UNIQUE INDEX one_active_run ON runs(active) WHERE active=1;
CREATE TABLE buckets(run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, template TEXT NOT NULL, idx INTEGER NOT NULL, seq TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(run_id,template,idx));
CREATE TABLE completion_buckets(run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, template TEXT NOT NULL, idx INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(run_id,template,idx));
CREATE TABLE aggregates(run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, kind TEXT NOT NULL, template TEXT NOT NULL, stage INTEGER NOT NULL, seq TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(run_id,kind,template,stage));
CREATE TABLE samples(run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, logical TEXT NOT NULL, attempt TEXT NOT NULL, seq TEXT NOT NULL, data BLOB NOT NULL, template TEXT NOT NULL, PRIMARY KEY(run_id,logical,attempt));
CREATE INDEX samples_scope ON samples(run_id,template,logical,attempt);
PRAGMA user_version=2;
COMMIT;`

func (s *Store) Start(ctx context.Context, snapshot domain.RunSnapshot, key string) (StartResult, error) {
	result := StartResult{RunID: snapshot.RunID}
	if key == "" || snapshot.RunID == "" || snapshot.SchemaVersion != 1 {
		return result, errors.New("history: invalid start")
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.CreatedAt); err != nil {
		return result, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return result, err
	}
	scopes, err := json.Marshal(snapshot.Draft.SelectedIDs)
	if err != nil {
		return result, err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var id string
		e := tx.QueryRowContext(ctx, "SELECT id FROM runs WHERE idem=?", key).Scan(&id)
		if e == nil {
			result = StartResult{RunID: id, Existing: true}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var active int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE active=1").Scan(&active); e != nil {
			return e
		}
		if active != 0 {
			return ErrActive
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO runs(id,idem,created,state,active,snapshot,url,mode,template_count,scopes) VALUES(?,?,?,'preparing',1,?,?,?,?,?)", snapshot.RunID, key, snapshot.CreatedAt, data, snapshot.Draft.URL, snapshot.Draft.Profile.Mode, len(snapshot.Draft.SelectedIDs), scopes)
		return e
	})
	return result, err
}

func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.fault != nil {
		if err = s.fault("begin"); err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if s.fault != nil {
		if err = s.fault("before_commit"); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	log.Print("Atomic history transaction acknowledged")
	if s.fault != nil {
		return s.fault("after_commit")
	}
	return nil
}

func (s *Store) Commit(ctx context.Context, cp engine.Checkpoint) error {
	terminal := cp.State == "completed" || cp.State == "stopped" || cp.State == "interrupted" || cp.State == "application_failed"
	if cp.RunID == "" || cp.Sequence == 0 || cp.Cut.IsZero() || (terminal != cp.Final && !(cp.State == "application_failed" && !cp.Final)) || len(cp.UnflushedCompletion) > 0 {
		return errors.New("history: invalid checkpoint")
	}
	if !terminal && cp.State != "running" && cp.State != "draining" && cp.State != "preparing" {
		return errors.New("history: invalid state")
	}
	if cp.Final && (cp.State == "completed" || cp.State == "stopped") && len(cp.Unfinished) > 0 {
		return errors.New("history: terminal work remains")
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	counts, err := summaryCounts(cp.Aggregates)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var seq uint64
		var completionSeq uint64
		var scopes []byte
		var previous []byte
		var active int
		e := tx.QueryRowContext(ctx, "SELECT seq,checkpoint,active,completion_seq,scopes FROM runs WHERE id=?", cp.RunID).Scan(&seq, &previous, &active, &completionSeq, &scopes)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if cp.Sequence < seq {
			return ErrStale
		}
		if cp.Sequence == seq {
			if string(data) == string(previous) {
				return nil
			}
			return ErrConflict
		}
		if active == 0 {
			return ErrConflict
		}
		if len(previous) > 0 {
			var old engine.Checkpoint
			if e = json.Unmarshal(previous, &old); e != nil {
				return e
			}
			if cp.Cut.Before(old.Cut) {
				return ErrStale
			}
		}
		if e = s.mergeCompletion(ctx, tx, cp, completionSeq, scopes); e != nil {
			return e
		}
		if s.fault != nil {
			if e = s.fault("after_completion"); e != nil {
				return e
			}
		}
		if e = s.upsertEvidence(ctx, tx, cp); e != nil {
			return e
		}
		if s.fault != nil {
			if e = s.fault("after_evidence"); e != nil {
				return e
			}
		}
		active = 1
		if cp.Final {
			active = 0
		}
		_, e = tx.ExecContext(ctx, "UPDATE runs SET seq=?,checkpoint=?,state=?,reason=?,active=?,cut=?,completion_seq=?,counts=? WHERE id=?", fmt.Sprint(cp.Sequence), data, cp.State, cp.Reason, active, cp.Cut.Format(time.RFC3339Nano), fmt.Sprint(cp.CompletionThrough), counts, cp.RunID)
		return e
	})
}

func (s *Store) upsertEvidence(ctx context.Context, tx *sql.Tx, cp engine.Checkpoint) error {
	seq := fmt.Sprint(cp.Sequence)
	seenBuckets := make(map[struct {
		template string
		index    int64
	}]bool, len(cp.Buckets))
	for _, v := range cp.Buckets {
		key := struct {
			template string
			index    int64
		}{v.TemplateID, v.Index}
		if v.Index < 0 || v.StartNS < 0 || v.EndNS < v.StartNS || seenBuckets[key] || v.Errors != 0 || v.Durations.Summary().Count != 0 {
			return errors.New("history: invalid bucket")
		}
		seenBuckets[key] = true
		// A cumulative base replay must not erase newer CPU-only evidence.
		var previous []byte
		e := tx.QueryRowContext(ctx, "SELECT data FROM buckets WHERE run_id=? AND template=? AND idx=?", cp.RunID, v.TemplateID, v.Index).Scan(&previous)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			var old engine.Bucket
			if e = json.Unmarshal(previous, &old); e != nil {
				return e
			}
			if old.CPUAt.After(v.CPUAt) {
				v.CPU, v.CPUAt, v.CPUIdentity, v.CPUReason = old.CPU, old.CPUAt, old.CPUIdentity, old.CPUReason
			}
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO buckets VALUES(?,?,?,?,?) ON CONFLICT(run_id,template,idx) DO UPDATE SET seq=excluded.seq,data=excluded.data", cp.RunID, v.TemplateID, v.Index, seq, b); e != nil {
			return e
		}
	}
	if len(cp.CPUAmendments) > 12 {
		return errors.New("history: CPU amendment bound exceeded")
	}
	var previousIndex int64 = -1
	for _, amendment := range cp.CPUAmendments {
		elapsed := amendment.At.Sub(cp.StartedAt)
		if amendment.Index <= previousIndex || amendment.Identity == "" || len(amendment.Identity) > 256 || len(amendment.Reason) > 512 || cp.StartedAt.IsZero() || amendment.At.After(cp.Cut) || elapsed < 0 || int64(elapsed/time.Second) != amendment.Index || amendment.Percent != nil && (math.IsNaN(*amendment.Percent) || math.IsInf(*amendment.Percent, 0) || *amendment.Percent < 0 || *amendment.Percent > 100) {
			return errors.New("history: invalid CPU amendment")
		}
		previousIndex = amendment.Index
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT data FROM buckets WHERE run_id=? AND template='' AND idx=?", cp.RunID, amendment.Index).Scan(&data); e != nil {
			return fmt.Errorf("history: CPU amendment missing base interval %d: %w", amendment.Index, e)
		}
		var base engine.Bucket
		if e := json.Unmarshal(data, &base); e != nil {
			return e
		}
		if !amendment.At.After(base.CPUAt) {
			continue
		}
		base.CPU, base.CPUAt, base.CPUIdentity, base.CPUReason = amendment.Percent, amendment.At, amendment.Identity, amendment.Reason
		data, e := json.Marshal(base)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE buckets SET seq=?,data=? WHERE run_id=? AND template='' AND idx=?", seq, data, cp.RunID, amendment.Index); e != nil {
			return e
		}
	}
	for _, v := range cp.Aggregates {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO aggregates VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,kind,template,stage) DO UPDATE SET seq=excluded.seq,data=excluded.data", cp.RunID, v.Kind, v.TemplateID, v.Stage, seq, b); e != nil {
			return e
		}
	}
	for _, v := range cp.Samples {
		if v.RunID != "" && v.RunID != cp.RunID {
			return errors.New("history: foreign sample")
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO samples VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,logical,attempt) DO UPDATE SET seq=excluded.seq,data=excluded.data,template=excluded.template", cp.RunID, fmt.Sprintf("%020d", v.Sequence), fmt.Sprintf("%020d", v.Attempt), seq, b, v.TemplateID); e != nil {
			return e
		}
	}
	return nil
}

func fileURI(absolute string) url.URL {
	path := filepath.ToSlash(absolute)
	// file://D:/ treats D: as an authority and SQLite rejects it;
	// the leading slash produces file:///D:/ while url.URL escapes #, %, and ?.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return url.URL{Scheme: "file", Path: path}
}
