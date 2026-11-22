package horseid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// exportDir — реальные файлы банка в репозитории (infra/reid-lab/export).
func exportDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "..", "infra", "reid-lab", "export")
	if _, err := os.Stat(filepath.Join(dir, "gallery.json")); err != nil {
		t.Skipf("реальный банк недоступен: %v", err)
	}
	return dir
}

// TestLoadGalleryIntegration проверяет загрузку реального export: размеры,
// число кличек, отсутствие частичных NaN в строках и корректность агрегатов.
func TestLoadGalleryIntegration(t *testing.T) {
	g, err := LoadGallery(exportDir(t))
	require.NoError(t, err)

	require.Len(t, g.Photos, 1114, "строк gallery.json = числу строк f32")
	require.Len(t, g.Horses, 15, "кличек в meta.json")
	require.InDelta(t, 0.5, g.Config.WeightHead, 1e-12)
	require.Equal(t, "top2", g.Config.Agg)
	require.InDelta(t, 20.0, g.Config.Temp, 1e-12)

	var headYes, headNo, bodyYes, bodyNo int
	perHorse := map[string]int{}
	for _, p := range g.Photos {
		if p.Head != nil {
			require.Len(t, p.Head, EmbedDim)
			headYes++
		} else {
			headNo++
		}
		if p.Body != nil {
			require.Len(t, p.Body, EmbedDim)
			bodyYes++
		} else {
			bodyNo++
		}
		perHorse[p.Horse]++
	}
	require.Equal(t, headYes+headNo, len(g.Photos))
	require.Equal(t, bodyYes+bodyNo, len(g.Photos))
	require.Positive(t, headNo, "в банке есть фото без головы (NaN-строки)")
	require.Zero(t, bodyNo, "у всех фото есть тело")
	require.Len(t, perHorse, 15)
}

// TestRankIntegrationSelf запрашивает реальным фото из банка: его лошадь
// должна оказаться первой, а проценты — просуммироваться в 100.
func TestRankIntegrationSelf(t *testing.T) {
	g, err := LoadGallery(exportDir(t))
	require.NoError(t, err)

	// Берём фото, у которого есть обе части.
	var idx = -1
	for i, p := range g.Photos {
		if p.Head != nil && p.Body != nil {
			idx = i
			break
		}
	}
	require.NotEqual(t, -1, idx)
	q := g.Photos[idx]

	got := g.Rank(Embedding{Head: q.Head, Body: q.Body, HasHead: true, HasBody: true})
	require.Len(t, got, 15)
	require.Equal(t, q.Horse, got[0].Horse, "запрос по эталону своей лошади должен быть первым")
	require.True(t, got[0].HasHead && got[0].HasBody)

	// Монотонность по score и сумма процентов.
	var sum float64
	for i, s := range got {
		sum += s.Pct
		if i > 0 {
			require.GreaterOrEqual(t, got[i-1].Score, s.Score)
		}
	}
	require.InDelta(t, 100.0, sum, 1e-6)
	// Свежее self-совпадение даёт score близко к 1.
	require.Greater(t, got[0].Score, 0.9)
}

// TestRankIntegrationNoHead проверяет fallback, когда запрос без головы:
// все головы отсутствуют, ranking идёт только по телу.
func TestRankIntegrationNoHead(t *testing.T) {
	g, err := LoadGallery(exportDir(t))
	require.NoError(t, err)
	q := g.Photos[0]
	got := g.Rank(Embedding{Body: q.Body, HasBody: true})
	for _, s := range got {
		require.False(t, s.HasHead, "без головы у запроса головы не считаются")
		require.True(t, s.HasBody)
		require.InDelta(t, s.Body, s.Score, 1e-12)
	}
}
