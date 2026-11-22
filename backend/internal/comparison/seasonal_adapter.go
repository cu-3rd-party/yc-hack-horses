package comparison

import (
	_ "embed"
	"encoding/binary"
	"math"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

const bottleneck = 64

// The final DataSphere adapter was trained only on raw MegaDescriptor-L-384
// bounding-box embeddings. The stored query and reference vectors stay raw.
// See infra/datasphere/export_seasonal_adapter.py for the pinned checkpoint.
//
//go:embed seasonal-adapter.f32
var adapterBytes []byte

var adapterMatrices = func() []float64 {
	const n = 2 * bottleneck * reid.Dimension
	if len(adapterBytes) != 4*n {
		panic("comparison: invalid embedded seasonal adapter")
	}
	weights := make([]float64, n)
	for i := range weights {
		weights[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(adapterBytes[i*4:])))
	}
	return weights
}()

// adaptBox projects a unit base vector using normalize(x + up(down(x))).
func adaptBox(x []float64) []float64 {
	if len(x) != reid.Dimension {
		return nil
	}
	const split = bottleneck * reid.Dimension
	down, up := adapterMatrices[:split], adapterMatrices[split:]
	var hidden [bottleneck]float64
	for i := range hidden {
		row := down[i*reid.Dimension : (i+1)*reid.Dimension]
		var sum float64
		for j, w := range row {
			sum += w * x[j]
		}
		hidden[i] = sum
	}
	out := make([]float64, len(x))
	for i, v := range x {
		row := up[i*bottleneck : (i+1)*bottleneck]
		for j, w := range row {
			v += w * hidden[j]
		}
		out[i] = v
	}
	return unit(out)
}
