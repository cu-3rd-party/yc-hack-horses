"""Экспорт YOLO11s (COCO) в ONNX для Go-бэкенда.

Использует ultralytics как в HANDOFF_GO.md: YOLO("yolo11s.pt").export(format="onnx", imgsz=640).
Имена входа/выхода обязаны остаться images / output0 (на них рассчитывает Go-бэкенд:
letterbox 640, вход 0..1 RGB, выход [1,84,8400]).

Запуск (из infra/reid-lab):
    .venv/bin/python export_onnx/export_yolo11s.py
Артефакт по умолчанию: /root/k8s/src/models/reid-lab/yolo11s.onnx
Переопределить каталог: REID_MODELS_DIR=/path .venv/bin/python export_onnx/export_yolo11s.py
"""
import os
import shutil
from pathlib import Path

import numpy as np

ARTIFACT_NAME = "yolo11s.onnx"
WEIGHTS_NAME = "yolo11s.pt"
DEFAULT_OUT_DIR = "/root/k8s/src/models/reid-lab"


def export(out_path: Path) -> None:
    from ultralytics import YOLO

    model = YOLO(WEIGHTS_NAME)
    produced = Path(model.export(format="onnx", imgsz=640))
    # ultralytics кладёт .onnx рядом с .pt-весами: переносим в целевой каталог.
    if produced.resolve() != out_path.resolve():
        shutil.move(str(produced), str(out_path))


def verify(out_path: Path) -> None:
    """ONNX vs сырой выход DetectionModel ultralytics на одном входе 640x640 (0..1).

    Экспортированный ONNX не содержит NMS: output0 — сырые предсказания [1,84,8400],
    ровно то, что отдаёт nn.Module yolov11 (net(x)[0]) до постобработки ultralytics.
    """
    import onnxruntime as ort
    import torch
    from ultralytics import YOLO

    sess = ort.InferenceSession(str(out_path), providers=["CPUExecutionProvider"])
    in_name = sess.get_inputs()[0].name
    out_name = sess.get_outputs()[0].name
    if in_name != "images" or out_name != "output0":
        raise SystemExit(f"НЕОЖИДАННЫЕ ИМЕНА: вход={in_name} выход={out_name}")
    x = np.random.RandomState(0).rand(1, 3, 640, 640).astype(np.float32)
    onnx_out = sess.run(["output0"], {"images": x})[0]

    net = YOLO(WEIGHTS_NAME).model.eval()
    with torch.inference_mode():
        ref = net(torch.from_numpy(x))[0].numpy()
    if onnx_out.shape != (1, 84, 8400):
        raise SystemExit(f"НЕОЖИДАННАЯ ФОРМА ВЫХОДА: {onnx_out.shape}, ждали (1, 84, 8400)")
    diff = float(np.abs(onnx_out - ref).max())
    print(f"[verify yolo] onnx {onnx_out.shape} vs ultralytics {ref.shape}; max|diff| = {diff:.3e}")
    if diff > 1e-3:
        raise SystemExit("РАСХОЖДЕНИЕ > 1e-3")


def main() -> None:
    out_dir = Path(os.environ.get("REID_MODELS_DIR", DEFAULT_OUT_DIR))
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / ARTIFACT_NAME
    export(out_path)
    verify(out_path)
    print(f"[yolo] {out_path} ({out_path.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
