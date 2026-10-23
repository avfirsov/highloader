package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/engine"
	"time"
)

func (s *Store) Recover(ctx context.Context, detected time.Time) ([]string, error) {
	if detected.IsZero() {
		return nil, errors.New("history: missing detection time")
	}
	ids := []string{}
	err := s.write(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, recordSQL+" WHERE active=1 ORDER BY id")
		if e != nil {
			return e
		}
		records := []Record{}
		for rows.Next() {
			r, e := decodeRecord(rows)
			if e != nil {
				rows.Close()
				return e
			}
			records = append(records, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, r := range records {
			start, e := time.Parse(time.RFC3339Nano, r.CreatedAt)
			if e != nil {
				return e
			}
			unknown := []engine.Unfinished{}
			if r.Checkpoint != nil {
				start = r.Checkpoint.Cut
				for _, unfinished := range r.Checkpoint.Unfinished {
					if !unfinished.OutcomeCommitted {
						unknown = append(unknown, unfinished)
					}
				}
			}
			recovery := Recovery{DetectedAt: detected, UnobservedFrom: start, Unknown: unknown}
			b, e := json.Marshal(recovery)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE runs SET active=0,state='interrupted',reason='process_interruption',recovery=? WHERE id=?", b, r.RunID); e != nil {
				return e
			}
			ids = append(ids, r.RunID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var active int
		e := tx.QueryRowContext(ctx, "SELECT active FROM runs WHERE id=?", id).Scan(&active)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if active != 0 {
			return ErrActive
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM runs WHERE id=?", id)
		return e
	})
}

func (s *Store) Clear(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var active int
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE active=1").Scan(&active); e != nil {
			return e
		}
		if active != 0 {
			return ErrActive
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM runs")
		return e
	})
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.reader.Close(), s.writer.Close())
}
