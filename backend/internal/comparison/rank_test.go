package comparison

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankingByIndividual(t *testing.T) {
	ref := func(horse, photo, version string, v ...float64) Reference {
		return Reference{HorseID: horse, PhotoID: photo, Descriptor: Descriptor{Version: version, Vector: v}}
	}
	query := Descriptor{Version: "v1", Vector: []float64{math.MaxFloat64, 0}}
	refs := []Reference{
		ref("b", "3", "v1", 1, 0), ref("a", "2", "v1", 1, 0), ref("a", "1", "v1", 2, 0),
		ref("a", "0", "v1", 0, 4), ref("c", "4", "v1", -1, 0), ref("d", "5", "v2", 1, 0),
		ref("e", "6", "v1", 1), ref("f", "7", "v1", 0, 0),
	}
	all := Rank(query, refs, 10)
	require.Len(t, all, 3)
	require.Equal(t, "a", all[0].HorseID)
	require.Equal(t, "1", all[0].BestPhotoID)
	require.Equal(t, "b", all[1].HorseID)
	require.Equal(t, -1., all[2].Similarity)
	require.Equal(t, all[:1], Rank(query, refs, 1))
	require.Empty(t, Rank(Descriptor{}, refs, 5))
}
