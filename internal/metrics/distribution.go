package metrics

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"
)

var ErrInvalid = errors.New("invalid metric observation")
var ErrOverflow = errors.New("metric count overflow")
var edges = makeEdges()

type Distribution struct {
	bins            map[int]uint64
	count           uint64
	min, max        time.Duration
	sum, correction float64
	limited         uint64
}
type Summary struct {
	Count             uint64 `json:"count,string"`
	Min, Max          *time.Duration
	MeanNS            *float64
	ResolutionLimited uint64 `json:"resolutionLimited,string"`
}
type sparseBin struct {
	Index int    `json:"index"`
	Count uint64 `json:"count,string"`
}
type wireDistribution struct {
	Version         int
	Bins            []sparseBin
	Count           uint64 `json:"count,string"`
	Min, Max        time.Duration
	Sum, Correction float64
	Limited         uint64 `json:"limited,string"`
}

func makeEdges() []int64 {
	e := make([]int64, 1001)
	for i := range e {
		e[i] = int64(i)
	}
	limit := int64(math.MaxInt64)/1000 + 1
	for n := int64(1000); n < limit; {
		step := n / 200
		if step < 1 {
			step = 1
		}
		n += step
		if n > limit {
			n = limit
		}
		e = append(e, n)
	}
	return e
}

func (d *Distribution) Add(v time.Duration) error {
	if v < 0 {
		return ErrInvalid
	}
	if d.count == math.MaxUint64 {
		return ErrOverflow
	}
	us := int64(v) / 1000
	if v%1000 != 0 {
		us++
	}
	index := sort.Search(len(edges), func(i int) bool { return edges[i] >= us })
	if d.bins == nil {
		d.bins = make(map[int]uint64)
	}
	d.bins[index]++
	if d.count == 0 || v < d.min {
		d.min = v
	}
	if v > d.max {
		d.max = v
	}
	d.count++
	if v < time.Microsecond {
		d.limited++
	}
	d.addSum(float64(v))
	return nil
}

func (d *Distribution) addSum(v float64) {
	y := v - d.correction
	t := d.sum + y
	d.correction = (t - d.sum) - y
	d.sum = t
}

func (d *Distribution) Merge(other *Distribution) error {
	if other == nil {
		return ErrInvalid
	}
	if math.MaxUint64-d.count < other.count {
		return ErrOverflow
	}
	if other.count == 0 {
		return nil
	}
	if d.bins == nil {
		d.bins = make(map[int]uint64)
	}
	// Snapshot moments also makes self-merge well-defined.
	sum, correction := other.sum, other.correction
	for i, n := range other.bins {
		d.bins[i] += n
	}
	if d.count == 0 || other.min < d.min {
		d.min = other.min
	}
	if other.max > d.max {
		d.max = other.max
	}
	d.count += other.count
	d.limited += other.limited
	d.addSum(sum)
	d.addSum(-correction)
	return nil
}

func (d *Distribution) Quantile(q float64) (time.Duration, bool) {
	if d.count == 0 || math.IsNaN(q) || q < 0 || q > 1 {
		return 0, false
	}
	rank := uint64(math.Ceil(q * float64(d.count)))
	if q == 1 || rank > d.count {
		rank = d.count
	}
	if rank == 0 {
		rank = 1
	}
	var n uint64
	for i, e := range edges {
		n += d.bins[i]
		if n >= rank {
			if e > math.MaxInt64/1000 {
				return time.Duration(math.MaxInt64), true
			}
			return time.Duration(e * 1000), true
		}
	}
	return 0, false
}

func (d *Distribution) Summary() Summary {
	s := Summary{Count: d.count, ResolutionLimited: d.limited}
	if d.count > 0 {
		lo, hi := d.min, d.max
		mean := (d.sum - d.correction) / float64(d.count)
		s.Min = &lo
		s.Max = &hi
		s.MeanNS = &mean
	}
	return s
}

func (d Distribution) MarshalJSON() ([]byte, error) {
	w := wireDistribution{Version: 1, Count: d.count, Min: d.min, Max: d.max, Sum: d.sum, Correction: d.correction, Limited: d.limited}
	for i, n := range d.bins {
		w.Bins = append(w.Bins, sparseBin{i, n})
	}
	sort.Slice(w.Bins, func(i, j int) bool { return w.Bins[i].Index < w.Bins[j].Index })
	return json.Marshal(w)
}

func (d *Distribution) UnmarshalJSON(data []byte) error {
	var w wireDistribution
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	if w.Version != 1 || w.Min < 0 || w.Max < w.Min || w.Limited > w.Count || math.IsNaN(w.Sum) || math.IsInf(w.Sum, 0) || math.IsNaN(w.Correction) || math.IsInf(w.Correction, 0) {
		return ErrInvalid
	}
	next := Distribution{bins: make(map[int]uint64), min: w.Min, max: w.Max, sum: w.Sum, correction: w.Correction, limited: w.Limited}
	last := -1
	for _, b := range w.Bins {
		if b.Index <= last || b.Index >= len(edges) || b.Count == 0 || math.MaxUint64-next.count < b.Count {
			return ErrInvalid
		}
		next.bins[b.Index] = b.Count
		next.count += b.Count
		last = b.Index
	}
	if next.count != w.Count {
		return ErrInvalid
	}
	if w.Count == 0 && (w.Min != 0 || w.Max != 0 || w.Sum != 0 || w.Correction != 0) {
		return ErrInvalid
	}
	if w.Count > 0 {
		loUS, hiUS := int64(w.Min)/1000, int64(w.Max)/1000
		if w.Min%1000 != 0 {
			loUS++
		}
		if w.Max%1000 != 0 {
			hiUS++
		}
		loBin := sort.Search(len(edges), func(i int) bool { return edges[i] >= loUS })
		hiBin := sort.Search(len(edges), func(i int) bool { return edges[i] >= hiUS })
		if w.Bins[0].Index != loBin || w.Bins[len(w.Bins)-1].Index != hiBin {
			return ErrInvalid
		}
		mean := (w.Sum - w.Correction) / float64(w.Count)
		if math.IsNaN(mean) || math.IsInf(mean, 0) || mean < float64(w.Min)-1 || mean > float64(w.Max)+1 {
			return ErrInvalid
		}
	}
	*d = next
	return nil
}

func MaxBins() int { return len(edges) }
