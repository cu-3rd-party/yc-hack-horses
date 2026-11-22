package horseid

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// testGallery собирает Gallery напрямую из фото (byHorse заполняется здесь).
func testGallery(horses []string, photos []GalleryPhoto) *Gallery {
	g := &Gallery{Horses: horses, Photos: photos, byHorse: map[string][]int{}}
	for i, p := range photos {
		g.byHorse[p.Horse] = append(g.byHorse[p.Horse], i)
	}
	g.Config = GalleryConfig{WeightHead: WeightHead, Agg: AggTop2, Temp: Temp}
	return g
}

func TestAggTop2(t *testing.T) {
	cases := []struct {
		name string
		v    []float64
		want float64
		ok   bool
	}{
		{"пусто", nil, 0, false},
		{"одно фото", []float64{0.7}, 0.7, true},
		{"два лучших из четырёх", []float64{0.2, 0.9, 0.5, 0.8}, 0.85, true},
		{"два элемента", []float64{1.0, 0.0}, 0.5, true},
		{"отрицательные", []float64{-0.5, -0.1, -0.9}, -0.30000000000000004, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := aggTop2(c.v)
			require.Equal(t, c.ok, ok)
			if c.ok {
				require.InDelta(t, c.want, got, 1e-9)
			}
		})
	}
}

type wantHorse struct {
	score   float64
	head    float64
	body    float64
	hasHead bool
	hasBody bool
}

func TestRank(t *testing.T) {
	// Кривая галерея: A — 3 фото head, 2 body; B — 1 head, 2 body; C — без головы.
	gallery3 := testGallery([]string{"A", "B", "C"}, []GalleryPhoto{
		{Horse: "A", Head: vec256(1.0), Body: vec256(1.0)},
		{Horse: "A", Head: vec256(0.8), Body: vec256(0.5)},
		{Horse: "A", Head: vec256(0.6)},
		{Horse: "B", Head: vec256(0.5), Body: vec256(1.0)},
		{Horse: "B", Body: vec256(0.2)},
		{Horse: "C", Body: vec256(0.9)},
	})

	cases := []struct {
		name  string
		g     *Gallery
		query Embedding
		order []string
		want  map[string]wantHorse
	}{
		{
			name:  "слияние 0.5/0.5 и top2",
			g:     gallery3,
			query: Embedding{Head: vec256(1.0), Body: vec256(1.0), HasHead: true, HasBody: true},
			order: []string{"C", "A", "B"},
			want: map[string]wantHorse{
				// head top2 = (1+0.8)/2 = 0.9; body top2 = (1+0.5)/2 = 0.75
				"A": {score: 0.825, head: 0.9, body: 0.75, hasHead: true, hasBody: true},
				// head = 0.5; body top2 = (1+0.2)/2 = 0.6
				"B": {score: 0.55, head: 0.5, body: 0.6, hasHead: true, hasBody: true},
				// головы нет -> score = body = 0.9
				"C": {score: 0.9, body: 0.9, hasHead: false, hasBody: true},
			},
		},
		{
			name:  "запрос без body, fallback на head",
			g:     gallery3,
			query: Embedding{Head: vec256(1.0), HasHead: true, HasBody: false},
			order: []string{"A", "B", "C"},
			want: map[string]wantHorse{
				"A": {score: 0.9, head: 0.9, hasHead: true, hasBody: false},
				"B": {score: 0.5, head: 0.5, hasHead: true, hasBody: false},
				"C": {score: 0.0, hasHead: false, hasBody: false},
			},
		},
		{
			name:  "запрос без head, fallback на body",
			g:     gallery3,
			query: Embedding{Body: vec256(1.0), HasHead: false, HasBody: true},
			order: []string{"C", "A", "B"},
			want: map[string]wantHorse{
				"A": {score: 0.75, body: 0.75, hasHead: false, hasBody: true},
				"B": {score: 0.6, body: 0.6, hasHead: false, hasBody: true},
				"C": {score: 0.9, body: 0.9, hasHead: false, hasBody: true},
			},
		},
		{
			name:  "одно фото — top2 равно значению",
			g:     testGallery([]string{"Solo"}, []GalleryPhoto{{Horse: "Solo", Head: vec256(0.7), Body: vec256(0.4)}}),
			query: Embedding{Head: vec256(1.0), Body: vec256(1.0), HasHead: true, HasBody: true},
			order: []string{"Solo"},
			want: map[string]wantHorse{
				"Solo": {score: 0.55, head: 0.7, body: 0.4, hasHead: true, hasBody: true},
			},
		},
		{
			name:  "лошадь без фото -> обе части отсутствуют",
			g:     testGallery([]string{"A", "Ghost"}, []GalleryPhoto{{Horse: "A", Head: vec256(1.0), Body: vec256(1.0)}}),
			query: Embedding{Head: vec256(1.0), Body: vec256(1.0), HasHead: true, HasBody: true},
			order: []string{"A", "Ghost"},
			want: map[string]wantHorse{
				"A":     {score: 1.0, head: 1.0, body: 1.0, hasHead: true, hasBody: true},
				"Ghost": {score: 0.0, hasHead: false, hasBody: false},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.g.Rank(c.query)
			require.Len(t, got, len(c.order))

			// порядок по убыванию score
			for i, h := range c.order {
				require.Equal(t, h, got[i].Horse, "позиция %d", i)
			}
			for i := 1; i < len(got); i++ {
				require.GreaterOrEqual(t, got[i-1].Score, got[i].Score)
			}

			// значения по лошадям
			byName := map[string]HorseScore{}
			var sumPct float64
			for _, s := range got {
				byName[s.Horse] = s
				sumPct += s.Pct
			}
			for name, w := range c.want {
				s := byName[name]
				require.InDelta(t, w.score, s.Score, 1e-9, "%s score", name)
				require.Equal(t, w.hasHead, s.HasHead, "%s hasHead", name)
				require.Equal(t, w.hasBody, s.HasBody, "%s hasBody", name)
				if w.hasHead {
					require.InDelta(t, w.head, s.Head, 1e-9, "%s head", name)
				}
				if w.hasBody {
					require.InDelta(t, w.body, s.Body, 1e-9, "%s body", name)
				}
			}
			require.InDelta(t, 100.0, sumPct, 1e-6, "сумма процентов должна быть 100")
		})
	}
}

// TestRankSoftmaxPercentages проверяет формулу softmax(Temp*score) и что
// топ-лошадь получает максимум процентов, а некалиброванный softmax суммируется в 100.
func TestRankSoftmaxPercentages(t *testing.T) {
	g := testGallery([]string{"A", "B"}, []GalleryPhoto{
		{Horse: "A", Body: vec256(1.0)},
		{Horse: "B", Body: vec256(0.0)},
	})
	got := g.Rank(Embedding{Body: vec256(1.0), HasBody: true})
	require.Equal(t, "A", got[0].Horse)

	// ожидаемые проценты: exp(20*1)/ (exp(20)+1) для A
	maxv := 1.0
	ea := math.Exp(g.Config.Temp * (1.0 - maxv))
	eb := math.Exp(g.Config.Temp * (0.0 - maxv))
	require.InDelta(t, ea/(ea+eb)*100, got[0].Pct, 1e-6)
	require.InDelta(t, eb/(ea+eb)*100, got[1].Pct, 1e-6)
	require.InDelta(t, 100.0, got[0].Pct+got[1].Pct, 1e-9)
}
