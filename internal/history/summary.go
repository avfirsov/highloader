package history

import (
	"encoding/json"
	"errors"
	"highloader/internal/engine"
	"math"
	"strconv"
)

type ResultCounts struct {
	Initial  uint64  `json:"initial,string"`
	Attempts uint64  `json:"attempts,string"`
	Errors   *uint64 `json:"errors,string"`
}

func summaryCounts(dimensions []engine.Dimension) ([]byte, error) {
	for _, d := range dimensions {
		if d.Kind != "overall" {
			continue
		}
		v := ResultCounts{Initial: d.Aggregate.Initial, Attempts: d.Aggregate.Started}
		if len(d.Aggregate.Attempts) >= 4 {
			var total uint64
			for _, raw := range d.Aggregate.Attempts[1:4] {
				n, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					return nil, err
				}
				if n > math.MaxUint64-total {
					return nil, errors.New("history: summary overflow")
				}
				total += n
			}
			v.Errors = &total
		}
		return json.Marshal(v)
	}
	return nil, nil
}
