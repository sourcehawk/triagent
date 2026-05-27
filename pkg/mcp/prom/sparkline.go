package prom

import "strings"

const sparklineWidth = 20

var sparklineRunes = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline downsamples vals to sparklineWidth buckets and renders each
// as one Unicode block-element rune. Buckets are min-max so the shape
// is preserved even when len(vals) >> sparklineWidth.
func sparkline(vals []float64) string {
	if len(vals) == 0 {
		return strings.Repeat("·", sparklineWidth)
	}
	buckets := downsample(vals, sparklineWidth)
	minVal, maxVal := buckets[0], buckets[0]
	for _, v := range buckets {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	rng := maxVal - minVal
	var b strings.Builder
	for _, v := range buckets {
		idx := 0
		if rng > 0 {
			frac := (v - minVal) / rng
			idx = int(frac * float64(len(sparklineRunes)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(sparklineRunes) {
				idx = len(sparklineRunes) - 1
			}
		}
		b.WriteRune(sparklineRunes[idx])
	}
	return b.String()
}

// downsample chooses one representative value per bucket so the result
// fits exactly `width` slots. Sparse inputs (len(vals) <= width) are
// stretched by nearest-left-neighbour repetition — this yields a
// visible staircase rather than a smooth ramp, which is acceptable for
// the 20-cell sparkline and avoids the noise that interpolation would
// introduce on already-coarse series. Dense inputs (len(vals) > width)
// collapse each bucket to its max so transient spikes survive the
// reduction.
func downsample(vals []float64, width int) []float64 {
	if len(vals) <= width {
		out := make([]float64, width)
		for i := 0; i < width; i++ {
			idx := i * len(vals) / width
			out[i] = vals[idx]
		}
		return out
	}
	out := make([]float64, width)
	bucket := float64(len(vals)) / float64(width)
	for i := 0; i < width; i++ {
		lo := int(float64(i) * bucket)
		hi := int(float64(i+1) * bucket)
		if hi > len(vals) {
			hi = len(vals)
		}
		maxVal := vals[lo]
		for _, v := range vals[lo:hi] {
			if v > maxVal {
				maxVal = v
			}
		}
		out[i] = maxVal
	}
	return out
}
