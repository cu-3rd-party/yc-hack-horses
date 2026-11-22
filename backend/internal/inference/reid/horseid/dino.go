//go:build cgo

// DINOv2-B/14: препроцессинг кропа (resize 224x224 bicubic без кропа, IMNET)
// и ONNX-инференс до L2-нормированного вектора 768.
//
// Эталон — infra/reid-lab/demo_app.py (raw_embed): T.Resize((224, 224), BICUBIC)
// → ToTensor → Normalize(IMNET) → backbone → F.normalize(dim=-1).
package horseid

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

const (
	// DinoSize — сторона входа DINOv2-B/14: resize (224, 224) без кропа.
	DinoSize = 224
	// dinoInput/dinoOutput — имена тензоров экспорта timm → ONNX.
	dinoInput  = "pixel_values"
	dinoOutput = "embedding"
	// bicubicSupport — радиус носителя кубического фильтра (в долях filterscale).
	bicubicSupport = 2.0
)

// ImageNet-нормировка, как T.Normalize(*IMNET) в demo_app.py.
var (
	imnetMean = [3]float32{0.485, 0.456, 0.406}
	imnetStd  = [3]float32{0.229, 0.224, 0.225}
)

// resampleKernel — одна выходная координата: первый входной индекс и веса.
type resampleKernel struct {
	first   int
	weights []float64
}

// bicubicWeight — кубическая свёртка Keys с a = -0.5, как в torchvision/Pillow
// (PIL использует ровно эти коэффициенты; a задаётся знаком при 1 <= |t| < 2):
//
//	w(t) = (1.5|t| - 2.5)|t|² + 1                при |t| < 1
//	w(t) = ((-0.5|t| + 2.5)|t| - 4)|t| + 2      при 1 <= |t| < 2
//	w(t) = 0                                     иначе
func bicubicWeight(t float64) float64 {
	x := math.Abs(t)
	switch {
	case x < 1:
		return ((1.5*x-2.5)*x)*x + 1
	case x < 2:
		return ((-0.5*x+2.5)*x-4)*x + 2
	default:
		return 0
	}
}

// resampleKernels повторяет Pillow precompute_coeffs для одного измерения:
// коэффициент масштаба scale = length/output; центр выходного пикселя в
// координатах входа center = (i+0.5)·scale; вклад входного пикселя j берётся
// по расстоянию (j+0.5-center)/filterscale, где filterscale = max(1, scale) —
// отсюда антиалиасинг при уменьшении. Веса нормируются на сумму.
func resampleKernels(length, output int) []resampleKernel {
	scale := float64(length) / float64(output)
	filterScale := math.Max(1, scale)
	support := bicubicSupport * filterScale
	step := 1 / filterScale
	table := make([]resampleKernel, output)
	for i := range table {
		center := (float64(i) + 0.5) * scale
		// Границы как в Pillow: int() отсекает, затем клип по [0, length).
		first := max(0, int(center-support+0.5))
		last := min(length, int(center+support+0.5))
		k := resampleKernel{first: first, weights: make([]float64, last-first)}
		var sum float64
		for j := range k.weights {
			w := bicubicWeight((float64(first+j) - center + 0.5) * step)
			k.weights[j] = w
			sum += w
		}
		if sum != 0 {
			for j := range k.weights {
				k.weights[j] /= sum
			}
		}
		table[i] = k
	}
	return table
}

// clampByte округляет и клипует в uint8 (Pillow 8bpc-путь: (int)(v+0.5), 0..255).
func clampByte(v float64) uint8 {
	return uint8(min(255, max(0, math.Round(v))))
}

// resampleRGB — разделимый двухпроходный bicubic (горизонталь, затем вертикаль)
// в interleaved RGB. Промежуточный проход квантуется в uint8, как у Pillow.
func resampleRGB(src image.Image, outW, outH int) []uint8 {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	pixels := make([]uint8, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// NRGBA, а не RGBA: Pillow convert("RGB") просто отбрасывает альфу.
			c := color.NRGBAModel.Convert(src.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			i := (y*w + x) * 3
			pixels[i], pixels[i+1], pixels[i+2] = c.R, c.G, c.B
		}
	}
	xs := resampleKernels(w, outW)
	ys := resampleKernels(h, outH)

	rows := make([]uint8, h*outW*3)
	for y := 0; y < h; y++ {
		srcRow := pixels[y*w*3 : (y+1)*w*3]
		dstRow := rows[y*outW*3 : (y+1)*outW*3]
		for x, k := range xs {
			base := k.first * 3
			for c := 0; c < 3; c++ {
				var sum float64
				for j, weight := range k.weights {
					sum += weight * float64(srcRow[base+j*3+c])
				}
				dstRow[x*3+c] = clampByte(sum)
			}
		}
	}

	out := make([]uint8, outH*outW*3)
	for y, k := range ys {
		for x := 0; x < outW; x++ {
			base := k.first * outW * 3
			for c := 0; c < 3; c++ {
				var sum float64
				for j, weight := range k.weights {
					sum += weight * float64(rows[base+j*outW*3+x*3+c])
				}
				out[(y*outW+x)*3+c] = clampByte(sum)
			}
		}
	}
	return out
}

// DinoPixels — вход DINOv2: NCHW float32 [3][224][224], каналы RGB,
// значения (byte/255 - mean) / std. Ресайз 224x224 bicubic без кропа
// (перекос пропорций допускается — так делает эталон).
func DinoPixels(img image.Image) ([]float32, error) {
	if img == nil || img.Bounds().Empty() {
		return nil, fmt.Errorf("%w: пустой кроп", inference.ErrInvalidImage)
	}
	rgb := resampleRGB(img, DinoSize, DinoSize)
	plane := DinoSize * DinoSize
	pixels := make([]float32, 3*plane)
	for i := 0; i < plane; i++ {
		for c := 0; c < 3; c++ {
			pixels[c*plane+i] = (float32(rgb[i*3+c])/255 - imnetMean[c]) / imnetStd[c]
		}
	}
	return pixels, nil
}

// Dino — ONNX-сессия DINOv2-B/14. Сессия не потокобезопасна, доступ под мьютексом.
type Dino struct {
	mu   sync.Mutex
	sess *ort.DynamicAdvancedSession
}

// OpenDino открывает сессию DINOv2 (вход pixel_values [b,3,224,224],
// выход embedding [b,768]). Требует инициализированного onnxruntime.
func OpenDino(modelPath string) (*Dino, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("%w: не задан путь к DINOv2 ONNX", inference.ErrModelUnavailable)
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrModelUnavailable, err)
	}
	defer opts.Destroy()
	if err = opts.SetIntraOpNumThreads(4); err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrModelUnavailable, err)
	}
	if err = opts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrModelUnavailable, err)
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath, []string{dinoInput}, []string{dinoOutput}, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: dinov2: %v", inference.ErrModelUnavailable, err)
	}
	return &Dino{sess: sess}, nil
}

// Embed считает эмбеддинг кропа: 768 значений с единичной L2-нормой
// (как raw_embed в demo_app.py — на вход ArcFace-проекции идёт именно он).
func (d *Dino) Embed(ctx context.Context, img image.Image) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pixels, err := DinoPixels(img)
	if err != nil {
		return nil, err
	}
	if d == nil || d.sess == nil {
		return nil, fmt.Errorf("%w: DINOv2 не инициализирован", inference.ErrModelUnavailable)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	input, err := ort.NewTensor(ort.NewShape(1, 3, int64(DinoSize), int64(DinoSize)), pixels)
	if err != nil {
		return nil, fmt.Errorf("%w: dinov2 input: %v", inference.ErrModelUnavailable, err)
	}
	defer input.Destroy()
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(DinoDim)))
	if err != nil {
		return nil, fmt.Errorf("%w: dinov2 output: %v", inference.ErrModelUnavailable, err)
	}
	defer output.Destroy()
	if err = d.sess.Run([]ort.Value{input}, []ort.Value{output}); err != nil {
		return nil, fmt.Errorf("%w: dinov2 run: %v", inference.ErrModelUnavailable, err)
	}
	vector := append([]float32(nil), output.GetData()...)
	if len(vector) != DinoDim {
		return nil, fmt.Errorf("horseid: DINOv2 вернул %d значений, ожидалось %d", len(vector), DinoDim)
	}
	if err = l2Normalize(vector); err != nil {
		return nil, err
	}
	return vector, ctx.Err()
}

// Close освобождает сессию.
func (d *Dino) Close() error {
	if d == nil || d.sess == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.sess.Destroy()
	d.sess = nil
	return err
}
