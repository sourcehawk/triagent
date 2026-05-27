package prom

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSparkline_LengthIsFixed(t *testing.T) {
	t.Parallel()
	vals := []float64{}
	for i := 0; i < 200; i++ {
		vals = append(vals, float64(i))
	}
	s := sparkline(vals)
	// 20 visual cells; each block-element rune is 3 bytes in UTF-8.
	assert.Equal(t, 20, runeCount(s))
}

func TestSparkline_AscendingHasIncreasingHeight(t *testing.T) {
	t.Parallel()
	vals := []float64{}
	for i := 0; i < 100; i++ {
		vals = append(vals, float64(i))
	}
	s := sparkline(vals)
	// First rune should be the lowest block; last should be the highest.
	runes := []rune(s)
	assert.Equal(t, '▁', runes[0])
	assert.Equal(t, '█', runes[len(runes)-1])
}

func TestSparkline_AllEqualFlat(t *testing.T) {
	t.Parallel()
	vals := []float64{0.5, 0.5, 0.5, 0.5}
	s := sparkline(vals)
	for _, r := range s {
		assert.Equal(t, '▁', r) // collapsed range → lowest tick
	}
}

func TestSparkline_EmptyInput(t *testing.T) {
	t.Parallel()
	assert.Equal(t, strings.Repeat("·", 20), sparkline(nil))
}

func TestSparkline_SingleElement(t *testing.T) {
	t.Parallel()
	s := sparkline([]float64{42})
	assert.Equal(t, 20, runeCount(s))
	for _, r := range s {
		assert.Equal(t, '▁', r, "single-element input collapses to flat low")
	}
}

func TestSparkline_NaNClampsToLow(t *testing.T) {
	t.Parallel()
	// Document the current NaN behaviour. NaN comparisons always return
	// false, so NaN does not propagate through the max-reduction in
	// downsample — NaN buckets carry NaN as-is. In the rune-mapping
	// loop, (NaN - minVal)/rng is NaN; int(NaN) underflows to
	// math.MinInt64, which the idx < 0 guard clamps to 0 so NaN cells
	// silently render as the lowest rune '▁'. Non-NaN values render
	// normally. Callers that need clean series must filter NaN upstream.
	vals := []float64{1, math.NaN(), 3, 5, 7}
	s := sparkline(vals)
	assert.Equal(t, 20, runeCount(s))
	// Last quarter must be the highest rune (value 7 maps to '█').
	runes := []rune(s)
	assert.Equal(t, '█', runes[len(runes)-1], "non-NaN high values still reach top rune")
	// NaN cells (second quarter, indices 4–7) must render as the lowest rune.
	for _, r := range runes[4:8] {
		assert.Equal(t, '▁', r, "NaN bucket clamps to lowest rune")
	}
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
