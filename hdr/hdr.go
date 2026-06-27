// Package hdr is a compact high-dynamic-range latency histogram with fixed
// relative precision across the whole range, so p99.9 and max are as
// trustworthy as p50. Values are nanoseconds. It is not the canonical
// HdrHistogram but follows the same log-bucket + linear-subbucket design.
package hdr

import (
	"math"
	"sort"
)

// Histogram records latencies in ns with ~1% relative precision up to ~1 hour.
type Histogram struct {
	// log-linear buckets: for each power-of-two magnitude we keep subBuckets
	// linear slots. precision ~ 1/subBuckets.
	subBits  uint
	subCount int
	counts   []uint64
	min      uint64
	max      uint64
	total    uint64
	sum      float64
}

// New returns a histogram with 2^subBits sub-buckets per magnitude (subBits=7
// gives ~0.8% precision), covering up to maxNs.
func New() *Histogram {
	const subBits = 7
	h := &Histogram{
		subBits:  subBits,
		subCount: 1 << subBits,
		min:      math.MaxUint64,
	}
	// 64 magnitudes is far more than enough for ns up to centuries.
	h.counts = make([]uint64, 64*h.subCount)
	return h
}

func (h *Histogram) index(v uint64) int {
	if v < uint64(h.subCount) {
		return int(v)
	}
	mag := 63 - bitsLeadingZeros(v) // floor(log2(v))
	shift := uint(mag) - h.subBits + 1
	sub := int((v >> shift) & uint64(h.subCount-1))
	return int(mag)*h.subCount + sub
}

func bitsLeadingZeros(v uint64) int {
	n := 0
	for i := 63; i >= 0; i-- {
		if v&(uint64(1)<<uint(i)) != 0 {
			return n
		}
		n++
	}
	return 64
}

// Record adds one latency sample in nanoseconds.
func (h *Histogram) Record(ns uint64) {
	i := h.index(ns)
	if i >= len(h.counts) {
		i = len(h.counts) - 1
	}
	h.counts[i]++
	h.total++
	h.sum += float64(ns)
	if ns < h.min {
		h.min = ns
	}
	if ns > h.max {
		h.max = ns
	}
}

// RecordCorrected adds a sample plus the synthetic backfill samples implied by
// coordinated omission: if the observed latency exceeds the expected interval,
// it backfills samples stepping down by interval. This is the HdrHistogram
// coordinated-omission correction.
func (h *Histogram) RecordCorrected(ns, expectedIntervalNs uint64) {
	h.Record(ns)
	if expectedIntervalNs == 0 || ns <= expectedIntervalNs {
		return
	}
	for missing := ns - expectedIntervalNs; missing >= expectedIntervalNs; missing -= expectedIntervalNs {
		h.Record(missing)
	}
}

// valueAt returns the representative ns value for a bucket index.
func (h *Histogram) valueAt(i int) uint64 {
	if i < h.subCount {
		return uint64(i)
	}
	mag := i / h.subCount
	sub := i % h.subCount
	shift := uint(mag) - h.subBits + 1
	return (uint64(h.subCount) + uint64(sub)) << shift
}

// Percentile returns the ns value at the given percentile (0..100).
func (h *Histogram) Percentile(p float64) uint64 {
	if h.total == 0 {
		return 0
	}
	target := uint64(math.Ceil(p / 100.0 * float64(h.total)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		if cum >= target {
			return h.valueAt(i)
		}
	}
	return h.max
}

func (h *Histogram) Count() uint64 { return h.total }
func (h *Histogram) Min() uint64 {
	if h.total == 0 {
		return 0
	}
	return h.min
}
func (h *Histogram) Max() uint64 { return h.max }
func (h *Histogram) Mean() uint64 {
	if h.total == 0 {
		return 0
	}
	return uint64(h.sum / float64(h.total))
}

// Snapshot is the reportable percentile set (ns).
type Snapshot struct {
	Count uint64 `json:"count"`
	P50   uint64 `json:"p50"`
	P90   uint64 `json:"p90"`
	P99   uint64 `json:"p99"`
	P999  uint64 `json:"p999"`
	P9999 uint64 `json:"p9999"`
	Max   uint64 `json:"max"`
	Mean  uint64 `json:"mean"`
}

func (h *Histogram) Snapshot() Snapshot {
	return Snapshot{
		Count: h.total,
		P50:   h.Percentile(50),
		P90:   h.Percentile(90),
		P99:   h.Percentile(99),
		P999:  h.Percentile(99.9),
		P9999: h.Percentile(99.99),
		Max:   h.Max(),
		Mean:  h.Mean(),
	}
}

// Merge folds another histogram into this one (for combining client goroutines).
func (h *Histogram) Merge(o *Histogram) {
	for i, c := range o.counts {
		h.counts[i] += c
	}
	h.total += o.total
	h.sum += o.sum
	if o.total > 0 {
		if o.min < h.min {
			h.min = o.min
		}
		if o.max > h.max {
			h.max = o.max
		}
	}
}

// MedianOf returns the median of a small slice of float64 (for variance across reps).
func MedianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
