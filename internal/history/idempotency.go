package history

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) FindStart(ctx context.Context, key string) (StartResult, error) {
	var id string
	err := s.reader.QueryRowContext(ctx, "SELECT id FROM runs WHERE idem=?", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return StartResult{}, ErrNotFound
	}
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{RunID: id, Existing: true}, nil
}
