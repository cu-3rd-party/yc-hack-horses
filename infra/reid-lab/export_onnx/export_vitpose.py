"""Экспорт ViTPose+ base (HF usyd-community/vitpose-plus-base) в ONNX для Go-бэкенда.

Модель обёрнута так, что граф принимает ТОЛЬКО pixel_values [b,3,256,192], а индекс
эксперта MoE dataset_index ЖЁСТКО зафиксирован = 3 (AP-10K) внутри графа (per-batch
вектор, заполненный тройками). Динамическая ось 0 у входа и выхода, opset 17.

=== Что возвращает граф ===
Единственный выход `heatmaps`: float32 [b, 17, 64, 48] — СЫРЫЕ тепмапы (без sigmoid),
17 точек, сетка 64 (высота) x 48 (ширина) = вход/4. Всё остальное — детерминированный
постпроцесс, который Go-бэкенд повторяет сам (см. ниже), ровно как
`VitPoseImageProcessor.post_process_pose_estimation` в transformers.

=== Предобработка входа (делает Go-бэкенд, здесь НЕ часть графа) ===
1. box животного в COCO (x0,y0,w,h) на полном изображении.
2. center, scale = box_to_center_and_scale(box, image_width=192, image_height=256,
   normalize_factor=200.0, padding_factor=1.25) — ВАЖНО: image_width/height берутся
   из целевого размера (192/256, aspect=0.75), а не из исходного кадра.
3. аффинный кроп box→(256,192): M = get_warp_matrix(rotation=0,
   size_input=center*2, size_dst=(192-1, 256-1), size_target=scale*200),
   затем scipy_warp_affine (order=1, = cv2.warpAffine INTER_LINEAR).
4. rescale 1/255 и normalize mean=(.485,.456,.406) std=(.229,.224,.225).

=== Декод выхода (повторяет post_process_pose_estimation, kernel_size=11) ===
heatmaps [17,64,48] (H=64, W=48):
1. get_keypoint_predictions: сплющить H*W, для каждой точки argmax → idx;
   x = idx % W, y = idx // W; score = max(heatmap). Где score <= 0 координаты → -1.
2. post_dark_unbiased_data_processing(coords, heatmaps, kernel=11): гаусс sigma=0.8
   radius=5, clip[0.001, 50], log, затем DARK/UDP-уточнение координат по гессиану.
3. transform_preds(coords, center, scale, output_size=(64,48)):
   scale *= 200; scale_y = scale[1]/(64-1); scale_x = scale[0]/(48-1);
   x_img = x*scale_x + center[0] - scale[0]*0.5;
   y_img = y*scale_y + center[1] - scale[1]*0.5.
Итог: 17 точек в пикселях исходного кадра + их score. В demo_app.py дополнительно
оставляются точки со score > 0.3 (голова: 0 нос, 1/2 глаза, 3/4 уши).

Запуск (из infra/reid-lab):
    .venv/bin/python export_onnx/export_vitpose.py
Артефакт по умолчанию: /root/k8s/src/models/reid-lab/vitpose-plus-base-256x192.onnx
Переопределить каталог: REID_MODELS_DIR=/path .venv/bin/python export_onnx/export_vitpose.py
"""
import os
from pathlib import Path

import numpy as np
import torch

HF_NAME = "usyd-community/vitpose-plus-base"
ARTIFACT_NAME = "vitpose-plus-base-256x192.onnx"
DATASET_INDEX = 3  # AP-10K, зафиксирован внутри графа
DEFAULT_OUT_DIR = "/root/k8s/src/models/reid-lab"


class VitPoseFixedDataset(torch.nn.Module):
    """Обёртка: forward принимает только pixel_values, dataset_index зафиксирован.

    Внутри строит per-batch вектор [b], заполненный DATASET_INDEX, и прокидывает его
    в MoE-эксперты backbone. Работает при динамическом batch.
    """

    def __init__(self, model: torch.nn.Module, dataset_index: int):
        super().__init__()
        self.model = model
        self.dataset_index = dataset_index

    def forward(self, pixel_values: torch.Tensor) -> torch.Tensor:
        ds = torch.full(
            (pixel_values.shape[0],), self.dataset_index, dtype=torch.long, device=pixel_values.device
        )
        out = self.model(pixel_values=pixel_values, dataset_index=ds)
        return out.heatmaps


def build_model() -> VitPoseFixedDataset:
    from transformers import VitPoseForPoseEstimation

    base = VitPoseForPoseEstimation.from_pretrained(HF_NAME).eval()
    return VitPoseFixedDataset(base, DATASET_INDEX).eval()


def export(out_path: Path) -> None:
    model = build_model()
    dummy = torch.randn(1, 3, 256, 192)
    torch.onnx.export(
        model,
        dummy,
        str(out_path),
        input_names=["pixel_values"],
        output_names=["heatmaps"],
        dynamic_axes={"pixel_values": {0: "b"}, "heatmaps": {0: "b"}},
        opset_version=17,
        dynamo=False,  # классический TorchScript-экспортёр: один файл, opset 17
    )


def verify(out_path: Path) -> None:
    """ONNX heatmaps vs прямой прогон обёрнутой модели, вход (1,3,256,192)."""
    import onnxruntime as ort

    model = build_model()
    sess = ort.InferenceSession(str(out_path), providers=["CPUExecutionProvider"])
    x = np.random.RandomState(0).randn(1, 3, 256, 192).astype(np.float32)
    onnx_out = sess.run(["heatmaps"], {"pixel_values": x})[0]
    with torch.inference_mode():
        ref = model(torch.from_numpy(x)).numpy()
    if onnx_out.shape != ref.shape:
        raise SystemExit(f"ФОРМА НЕ СОВПАЛА: onnx {onnx_out.shape} vs torch {ref.shape}")
    diff = float(np.abs(onnx_out - ref).max())
    print(f"[verify vitpose] onnx {onnx_out.shape} vs torch {ref.shape}; max|diff| = {diff:.3e}")
    if diff > 1e-3:
        raise SystemExit("РАСХОЖДЕНИЕ > 1e-3")

    # Динамический batch: batch=2 не должен падать и давать ту же форму.
    x2 = np.random.RandomState(1).randn(2, 3, 256, 192).astype(np.float32)
    out2 = sess.run(["heatmaps"], {"pixel_values": x2})[0]
    print(f"[verify vitpose] dynamic batch: вход (2,3,256,192) → выход {out2.shape}")
    if out2.shape != (2, 17, 64, 48):
        raise SystemExit(f"НЕВЕРНАЯ ФОРМА ПРИ BATCH=2: {out2.shape}")


def main() -> None:
    out_dir = Path(os.environ.get("REID_MODELS_DIR", DEFAULT_OUT_DIR))
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / ARTIFACT_NAME
    export(out_path)
    verify(out_path)
    print(f"[vitpose] {out_path} ({out_path.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
