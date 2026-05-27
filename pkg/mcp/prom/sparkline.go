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
	min, max := buckets[0], buckets[0]
	for _, v := range buckets {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rng := max - min
	var b strings.Builder
	for _, v := range buckets {
		idx := 0
		if rng > 0 {
			frac := (v - min) / rng
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

// downsample chooses one representative value per bucket. Uses the bucket
// midpoint by default; with very long inputs (>width*2) it picks the
// max of each bucket so spikes survive.
func downsample(vals []float64, width int) []float64 {
	if len(vals) <= width {
		out := make([]float64, width)
		// Stretch: repeat each value evenly.
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
		max := vals[lo]
		for _, v := range vals[lo:hi] {
			if v > max {
				max = v
			}
		}
		out[i] = max
	}
	return out
}
