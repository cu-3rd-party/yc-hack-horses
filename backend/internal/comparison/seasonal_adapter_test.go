package comparison

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/stretchr/testify/require"
)

func TestFinalSeasonalBoxAdapter(t *testing.T) {
	require.Equal(t, "0e4a7ee99a2937d8544be926238711aae6265f8b0105e78ce1094e0045fd9662",
		fmt.Sprintf("%x", sha256.Sum256(adapterBytes)))
	a, b := make([]float64, reid.Dimension), make([]float64, reid.Dimension)
	a[0], a[96], a[1204] = .2, -.71, 1.1
	b[80], b[120], b[1310] = .63, -1.13, .85
	beforeA, beforeB := slices.Clone(a), slices.Clone(b)
	for _, c := range []struct {
		version string
		cosine  float64
	}{
		// Calculated independently with torch and the final three-epoch checkpoint.
		{reid.BoxVersion, -.0005090795457363129},
		{reid.Version, 0},
		{"other", 0},
	} {
		q := Descriptor{Version: c.version, Vector: a}
		r := Reference{HorseID: "a", PhotoID: "b", Descriptor: Descriptor{Version: c.version, Vector: b}}
		matches := Rank(q, []Reference{r}, 1)
		require.Len(t, matches, 1)
		require.InDelta(t, c.cosine, matches[0].Similarity, 1e-8, c.version)
	}
	require.Equal(t, beforeA, a)
	require.Equal(t, beforeB, b)
	require.Empty(t, Rank(Descriptor{Version: reid.BoxVersion, Vector: []float64{1, 0}}, nil, 5))
}

func TestSeasonalBoxExampleRepresentsTheHorse(t *testing.T) {
	x := make([]float64, reid.Dimension)
	y := make([]float64, reid.Dimension)
	middle := make([]float64, reid.Dimension)
	x[0], y[1] = 1, 1
	middle[0], middle[1] = 1, 1
	ref := func(id string, vector []float64) Reference {
		return Reference{
			HorseID: "horse", PhotoID: id,
			Descriptor: Descriptor{Version: reid.BoxVersion, CaptureTime: "2025-04-02", Vector: vector},
		}
	}
	refs := []Reference{ref("query-like", x), ref("representative", middle), ref("third", y)}
	got := Rank(refs[0].Descriptor, refs, 1)
	require.Len(t, got, 1)
	require.Equal(t, "representative", got[0].BestPhotoID)
}
