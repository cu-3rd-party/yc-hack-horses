package reid

// Эталоны порта viewpoint.py: синтетические разности около оси (1,0),
// сторона по порогу, маска сравнения.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFitSideAxisFindsFacingDirection(t *testing.T) {
	d := [][]float64{
		{1, 0}, {0.9, 0.1}, {-1, 0}, {-0.9, -0.1}, {0.2, 0.3}, {-0.2, 0.1},
	}
	axis := FitSideAxis(d)
	require.NotNil(t, axis)
	// numpy-эталон на том же входе: |axis| = [0.9981311742, 0.0611077668].
	// Знак оси произволен (PCA-направление), поэтому сравниваем модули.
	require.True(t, math.Abs(math.Abs(axis[0])-0.9981311742) < 1e-6, "axis[0]=%v", axis[0])
	require.True(t, math.Abs(math.Abs(axis[1])-0.0611077668) < 1e-6, "axis[1]=%v", axis[1])

	// инвариант знака: ось для -d совпадает с точностью до направления
	neg := make([][]float64, len(d))
	for i, row := range d {
		neg[i] = []float64{-row[0], -row[1]}
	}
	axisNeg := FitSideAxis(neg)
	dot := axis[0]*axisNeg[0] + axis[1]*axisNeg[1]
	require.True(t, math.Abs(math.Abs(dot)-1) < 1e-6, "dot=%v", dot)
}

func TestAssignSideAndMask(t *testing.T) {
	sides := AssignSide([]float64{2, 1.9, -2, -1.9, 0.01, -0.01}, 0.15)
	require.Equal(t, []string{"A", "A", "B", "B", "unknown", "unknown"}, sides)

	require.True(t, SameSideAllowed("A", "A"))
	require.False(t, SameSideAllowed("A", "B"))
	// unknown с любой стороны сравнивается со всеми
	require.True(t, SameSideAllowed("unknown", "B"))
	require.True(t, SameSideAllowed("A", "unknown"))
	require.True(t, SameSideAllowed("", "A"))

	require.Equal(t, "A", SideOf(1, 0.5))
	require.Equal(t, "B", SideOf(-1, 0.5))
	require.Equal(t, "unknown", SideOf(0.1, 0.5))
}

func TestQuantileLinearInterpolation(t *testing.T) {
	require.Equal(t, 1.0, linearQuantile([]float64{1, 2, 3, 4}, 0))
	require.Equal(t, 4.0, linearQuantile([]float64{1, 2, 3, 4}, 1))
	require.True(t, math.Abs(linearQuantile([]float64{1, 2, 3, 4}, 0.25)-1.75) < 1e-9)
}
