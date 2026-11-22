package comparison

import (
	"math"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
	"github.com/stretchr/testify/require"
)

// partAt — единичный вектор длины EmbedDim, косинус которого с осью e0 равен cos
// (cos=1 — часть совпадает с запросом, cos=0 — ортогональна, вектор не нулевой).
func partAt(cos float64) []float64 {
	v := make([]float64, horseid.EmbedDim)
	v[0] = cos
	v[1] = math.Sqrt(math.Max(0, 1-cos*cos))
	return v
}

// zeroPart — нулевая половина: так кодируется отсутствие части.
func zeroPart() []float64 { return make([]float64, horseid.EmbedDim) }

// desc склеивает половины head и body в дескриптор версии horseid.
func desc(head, body []float64) []float64 {
	return append(append(make([]float64, 0, 2*horseid.EmbedDim), head...), body...)
}

func horseRef(horse, photo string, head, body []float64) Reference {
	return Reference{HorseID: horse, PhotoID: photo, Descriptor: Descriptor{Version: horseid.Version, Vector: desc(head, body)}}
}

// Запрос со «здоровыми» обеими частями: head и body точно совпадают с e0.
func queryHorseID(head, body []float64) Descriptor {
	return Descriptor{Version: horseid.Version, Vector: desc(head, body)}
}

// TestRankHorseIDTop2ByPart проверяет агрегацию по частям: счёт лошади по head —
// среднее двух лучших head-сходств по её фото, то же по body, затем 0.5/0.5.
// Это отличает реализацию от «сначала слить per-photo, потом top2»: здесь
// head-счёт .9 (top2 из 1, .8, 0), body-счёт .5 (top2 из 0, 0, 1), итог .7.
func TestRankHorseIDTop2ByPart(t *testing.T) {
	q := queryHorseID(partAt(1), partAt(1))
	refs := []Reference{
		horseRef("a", "1", partAt(1), partAt(0)),
		horseRef("a", "2", partAt(0.8), partAt(0)),
		horseRef("a", "3", partAt(0), partAt(1)),
	}
	got := Rank(q, refs, 10)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].HorseID)
	require.InDelta(t, 0.7, got[0].Similarity, 1e-9)
	// per-photo: фото 1 → .5, фото 2 → .4, фото 3 → .5; ничья, меньший id.
	require.Equal(t, "1", got[0].BestPhotoID)
}

// TestRankHorseIDQueryMissingHead: нет head у запроса — берётся body.
func TestRankHorseIDQueryMissingHead(t *testing.T) {
	q := queryHorseID(zeroPart(), partAt(1))
	refs := []Reference{horseRef("a", "1", partAt(1), partAt(1))}
	got := Rank(q, refs, 10)
	require.Len(t, got, 1)
	require.InDelta(t, 1.0, got[0].Similarity, 1e-9)
}

// TestRankHorseIDMissingParts — отсутствие части (нулевая половина) у запроса и у
// эталона: считается только имеющаяся часть, 0.5/0.5 не применяется.
func TestRankHorseIDMissingParts(t *testing.T) {
	q := queryHorseID(partAt(0.6), partAt(1))
	refs := []Reference{
		horseRef("a", "1", partAt(1), zeroPart()), // body нет → только head .6
		horseRef("b", "1", zeroPart(), partAt(1)), // head нет → только body 1
	}
	got := Rank(q, refs, 10)
	require.Len(t, got, 2)
	require.Equal(t, "b", got[0].HorseID)
	require.InDelta(t, 1.0, got[0].Similarity, 1e-9)
	require.Equal(t, "a", got[1].HorseID)
	require.InDelta(t, 0.6, got[1].Similarity, 1e-9)
}

// TestRankHorseIDSorting — сортировка по score убыв., ничья — по HorseID.
func TestRankHorseIDSorting(t *testing.T) {
	q := queryHorseID(partAt(1), partAt(1))
	refs := []Reference{
		horseRef("h1", "1", partAt(0.1), partAt(0.1)),
		horseRef("h2", "1", partAt(0.9), partAt(0.9)),
		horseRef("h3", "1", partAt(0.5), partAt(0.5)),
		horseRef("h4", "1", partAt(0.5), partAt(0.5)),
	}
	got := Rank(q, refs, 10)
	require.Equal(t, []string{"h2", "h3", "h4", "h1"}, []string{got[0].HorseID, got[1].HorseID, got[2].HorseID, got[3].HorseID})
	require.InDelta(t, 0.9, got[0].Similarity, 1e-9)
	require.Len(t, Rank(q, refs, 2), 2)
}

// TestRankHorseIDOldVersionUntouched — дескриптор другой версии той же «двойной»
// длины идёт прежним путём (одно сходство по всему вектору, максимум по фото).
func TestRankHorseIDOldVersionUntouched(t *testing.T) {
	q := Descriptor{Version: "megadescriptor", Vector: []float64{1, 0}}
	refs := []Reference{
		{HorseID: "a", PhotoID: "1", Descriptor: Descriptor{Version: "megadescriptor", Vector: []float64{1, 0}}},
		{HorseID: "a", PhotoID: "2", Descriptor: Descriptor{Version: "megadescriptor", Vector: []float64{0, 1}}},
	}
	got := Rank(q, refs, 10)
	require.Len(t, got, 1)
	require.InDelta(t, 1.0, got[0].Similarity, 1e-9)
	require.Equal(t, "1", got[0].BestPhotoID)
}

// TestRankHorseIDDegenerate — некорректная длина и полностью нулевые векторы
// не дают кандидатов, а помеченный удалённым эталон не участвует.
func TestRankHorseIDDegenerate(t *testing.T) {
	require.Empty(t, Rank(queryHorseID(zeroPart(), zeroPart()), []Reference{horseRef("a", "1", partAt(1), partAt(1))}, 10))
	require.Empty(t, Rank(Descriptor{Version: horseid.Version, Vector: make([]float64, 10)}, []Reference{horseRef("a", "1", partAt(1), partAt(1))}, 10))
	now := time.Now()
	ref := horseRef("a", "1", partAt(1), partAt(1))
	ref.InvalidatedAt = &now
	require.Empty(t, Rank(queryHorseID(partAt(1), partAt(1)), []Reference{ref}, 10))
}
