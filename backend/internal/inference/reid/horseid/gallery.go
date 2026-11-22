package horseid

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// GalleryPhoto — одно фото банка: метаданные и эталонные эмбеддинги по частям.
// Head или Body равны nil, если для этого фото соответствующей части нет
// (строка gallery_*.f32 содержит NaN).
type GalleryPhoto struct {
	PhotoID string
	Horse   string
	Date    string
	View    View
	Head    []float64
	Body    []float64
}

// GalleryConfig — параметры ранжирования, считанные из meta.json.
// Отсутствующие или некорректные поля заменяются константами контракта.
type GalleryConfig struct {
	WeightHead float64 // вес head при слиянии score = w*head + (1-w)*body
	Agg        string  // способ агрегации по лошади ("top2")
	Temp       float64 // температура softmax для процентов
}

// Gallery — загруженный банк эталонов (gallery_dinov2_head/body.f32 + gallery.json + meta.json).
type Gallery struct {
	Dir    string
	Config GalleryConfig
	// Horses — список кличек в порядке из meta.json.
	Horses []string
	// Photos — эталоны в порядке строк f32-файлов.
	Photos []GalleryPhoto

	byHorse map[string][]int
}

// galleryRecord — запись gallery.json (порядок = строки f32-файлов).
type galleryRecord struct {
	PhotoID string `json:"photo_id"`
	Horse   string `json:"horse"`
	Date    string `json:"date"`
	View    string `json:"view"`
}

// galleryMeta — нужные поля export/meta.json.
type galleryMeta struct {
	Config struct {
		W   float64 `json:"w"`
		Agg string  `json:"agg"`
	} `json:"config"`
	Horses      []string `json:"horses"`
	Dim         int      `json:"dim"`
	Temperature float64  `json:"temperature"`
}

// LoadGallery читает банк эталонов из каталога dir (обычно infra/reid-lab/export).
// Проверяются размеры файлов, согласованность строк и размерность EmbedDim.
func LoadGallery(dir string) (*Gallery, error) {
	records, err := loadGalleryJSON(filepath.Join(dir, "gallery.json"))
	if err != nil {
		return nil, err
	}
	head, err := loadF32Rows(filepath.Join(dir, "gallery_dinov2_head.f32"), len(records))
	if err != nil {
		return nil, err
	}
	body, err := loadF32Rows(filepath.Join(dir, "gallery_dinov2_body.f32"), len(records))
	if err != nil {
		return nil, err
	}
	meta, err := loadGalleryMeta(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	if meta.Dim != 0 && meta.Dim != EmbedDim {
		return nil, fmt.Errorf("horseid: meta.json dim=%d, ожидается %d", meta.Dim, EmbedDim)
	}

	g := &Gallery{
		Dir: dir,
		Config: GalleryConfig{
			WeightHead: meta.Config.W,
			Agg:        meta.Config.Agg,
			Temp:       meta.Temperature,
		},
		Horses:  meta.Horses,
		Photos:  make([]GalleryPhoto, len(records)),
		byHorse: make(map[string][]int, len(meta.Horses)),
	}
	if !(g.Config.WeightHead > 0 && g.Config.WeightHead <= 1) {
		g.Config.WeightHead = WeightHead
	}
	if g.Config.Agg == "" {
		g.Config.Agg = AggTop2
	}
	if !(g.Config.Temp > 0) {
		g.Config.Temp = Temp
	}

	for i, r := range records {
		p := GalleryPhoto{
			PhotoID: r.PhotoID,
			Horse:   r.Horse,
			Date:    r.Date,
			View:    View(r.View),
		}
		p.Head = head[i]
		p.Body = body[i]
		g.Photos[i] = p
		g.byHorse[p.Horse] = append(g.byHorse[p.Horse], i)
	}
	if len(g.Horses) == 0 {
		// meta.json без списка кличек — восстанавливаем из фото в устойчивом порядке.
		for h := range g.byHorse {
			g.Horses = append(g.Horses, h)
		}
		sort.Strings(g.Horses)
	}
	return g, nil
}

// loadGalleryJSON читает записи gallery.json (порядок соответствует строкам f32).
func loadGalleryJSON(path string) ([]galleryRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("horseid: чтение %s: %w", path, err)
	}
	var records []galleryRecord
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, fmt.Errorf("horseid: разбор %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("horseid: %s пуст", path)
	}
	return records, nil
}

// loadGalleryMeta читает конфиг meta.json.
func loadGalleryMeta(path string) (galleryMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return galleryMeta{}, fmt.Errorf("horseid: чтение %s: %w", path, err)
	}
	var m galleryMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return galleryMeta{}, fmt.Errorf("horseid: разбор %s: %w", path, err)
	}
	return m, nil
}

// loadF32Rows читает матрицу [rows][EmbedDim] little-endian float32.
// Строка с не-финитным значением (NaN или ±Inf, как np.isfinite в demo_app.py)
// считается отсутствующей частью и возвращается как nil.
func loadF32Rows(path string, rows int) ([][]float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("horseid: чтение %s: %w", path, err)
	}
	want := rows * EmbedDim * 4
	if len(b) != want {
		return nil, fmt.Errorf("horseid: %s: %d байт, ожидается %d (%d строк по %d)",
			path, len(b), want, rows, EmbedDim)
	}
	out := make([][]float64, rows)
	for i := 0; i < rows; i++ {
		row := make([]float64, EmbedDim)
		absent := false
		for k := 0; k < EmbedDim; k++ {
			v := math.Float32frombits(binary.LittleEndian.Uint32(b[(i*EmbedDim+k)*4:]))
			if f := float64(v); math.IsNaN(f) || math.IsInf(f, 0) {
				absent = true
				break
			}
			row[k] = float64(v)
		}
		if !absent {
			out[i] = row
		}
	}
	return out, nil
}
