package prom

import (
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

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
