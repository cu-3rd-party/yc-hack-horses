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
		ref("e", "6", "v1", 1), ref("f", "7", "v1", 0, 0), ref("a", "8", "v1", 0, 0),
		ref("a", "9", "v2", -1, 0), ref("a", "10", "v1", 1),
	}
	all := Rank(query, refs, 10)
	require.Len(t, all, 3)
	require.Equal(t, "b", all[0].HorseID)
	require.Equal(t, 1., all[0].Similarity)
	require.Equal(t, "a", all[1].HorseID)
	require.InDelta(t, 2/math.Sqrt(5), all[1].Similarity, 1e-12)
	require.Equal(t, "1", all[1].BestPhotoID)
	require.Equal(t, -1., all[2].Similarity)
	require.Equal(t, all[:1], Rank(query, refs, 1))
	require.Empty(t, Rank(Descriptor{}, refs, 5))
}

func TestFiveRecentPerSeason(t *testing.T) {
	q := Descriptor{Version: "v", Vector: []float64{1, 0}, CaptureTime: "2025-01-15"}
	refs := []Reference{}
	add := func(id, day string, x, y float64) {
		refs = append(refs, Reference{HorseID: "horse", PhotoID: id, SHA: id, Descriptor: Descriptor{Version: "v", CaptureTime: day, Vector: []float64{x, y}}})
	}
	add("old", "2022-01-01", 1, 0)
	add("summer", "2024-07-02", 1, 0)
	for i := 0; i < 5; i++ {
		add(string(rune('a'+i)), "2024-12-01", 0, 1)
	}
	found := Rank(q, refs, 1)[0]
	require.Equal(t, 5, found.ReferenceCount)
	require.Equal(t, 0., found.Similarity)
	require.False(t, found.SeasonFallback)
	q.CaptureTime = "2025-04-01"
	found = Rank(q, refs, 1)[0]
	require.True(t, found.SeasonFallback)
	require.Equal(t, 6, found.ReferenceCount)
	require.InDelta(t, 1/math.Sqrt(26), found.Similarity, 1e-12)
	q.CaptureTime = ""
	require.Equal(t, found, Rank(q, refs, 1)[0])
}
