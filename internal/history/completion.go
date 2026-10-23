package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/engine"
	"math"
)

func (s *Store) mergeCompletion(ctx context.Context, tx *sql.Tx, cp engine.Checkpoint, cursor uint64, scopes []byte) error {
	var selected []string
	if err := json.Unmarshal(scopes, &selected); err != nil {
		return err
	}
	known := map[string]bool{"": true}
	for _, id := range selected {
		known[id] = true
	}
	var previous uint64
	for _, batch := range cp.CompletionBatches {
		if batch.Version != 1 || batch.Sequence == 0 || batch.Sequence <= previous || len(batch.Deltas) == 0 {
			return errors.New("history: invalid completion batch")
		}
		previous = batch.Sequence
		for i, d := range batch.Deltas {
			if d.Index < 0 || d.Index > math.MaxInt64/int64(1000000000) || !known[d.TemplateID] {
				return errors.New("history: invalid completion scope/index")
			}
			if i > 0 {
				p := batch.Deltas[i-1]
				if d.TemplateID < p.TemplateID || (d.TemplateID == p.TemplateID && d.Index <= p.Index) {
					return errors.New("history: duplicate or unsorted completion key")
				}
			}
			if err := d.Value.Validate(); err != nil {
				return err
			}
		}
		if batch.Sequence <= cursor {
			continue
		}
		if cursor == math.MaxUint64 || batch.Sequence != cursor+1 {
			return errors.New("history: completion sequence gap")
		}
		for _, d := range batch.Deltas {
			if err := mergeDelta(ctx, tx, cp.RunID, d); err != nil {
				return err
			}
		}
		cursor = batch.Sequence
	}
	if cp.CompletionThrough != cursor {
		return errors.New("history: completion cursor does not match coverage")
	}
	return nil
}

func mergeDelta(ctx context.Context, tx *sql.Tx, id string, d engine.CompletionDelta) error {
	var raw []byte
	var value engine.CompletionValue
	err := tx.QueryRowContext(ctx, "SELECT data FROM completion_buckets WHERE run_id=? AND template=? AND idx=?", id, d.TemplateID, d.Index).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &value); err != nil {
			return err
		}
	}
	if err = value.Merge(d.Value); err != nil {
		return err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO completion_buckets VALUES(?,?,?,?) ON CONFLICT(run_id,template,idx) DO UPDATE SET data=excluded.data", id, d.TemplateID, d.Index, raw)
	return err
}
