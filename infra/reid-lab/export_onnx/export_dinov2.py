"""Экспорт DINOv2-B/14 (timm vit_base_patch14_dinov2.lvd142m) в ONNX для Go-бэкенда.

Ровно тот же код модели, что в HANDOFF_GO.md и demo_app.py: pretrained, num_classes=0,
img_size=224. Вход pixel_values [b,3,224,224] (resize 224x224 BICUBIC без кропа,
ImageNet mean/std), выход embedding [b,768] — CLS-токен после финальной norm (без L2).
Динамическая ось 0 у входа и выхода, opset 17.

Запуск (из infra/reid-lab):
    .venv/bin/python export_onnx/export_dinov2.py
Артефакт по умолчанию: /root/k8s/src/models/reid-lab/dinov2-b14-224.onnx
Переопределить каталог: REID_MODELS_DIR=/path .venv/bin/python export_onnx/export_dinov2.py
"""
import os
from pathlib import Path

import numpy as np
import torch
import timm

WEIGHTS_NAME = "vit_base_patch14_dinov2.lvd142m"
ARTIFACT_NAME = "dinov2-b14-224.onnx"
DEFAULT_OUT_DIR = "/root/k8s/src/models/reid-lab"


def build_model() -> torch.nn.Module:
    m = timm.create_model(WEIGHTS_NAME, pretrained=True, num_classes=0, img_size=224)
    return m.eval()


def export(out_path: Path) -> None:
    model = build_model()
    dummy = torch.randn(1, 3, 224, 224)
    torch.onnx.export(
        model,
        dummy,
        str(out_path),
        input_names=["pixel_values"],
        output_names=["embedding"],
        dynamic_axes={"pixel_values": {0: "b"}, "embedding": {0: "b"}},
        opset_version=17,
        dynamo=False,  # классический TorchScript-экспортёр: один файл, opset 17 как в HANDOFF
    )


def verify(out_path: Path) -> None:
    """ONNX vs прямой timm-прогон: та же модель, вход (2,3,224,224), max abs diff."""
    import onnxruntime as ort

    model = build_model()
    sess = ort.InferenceSession(str(out_path), providers=["CPUExecutionProvider"])
    x = np.random.RandomState(0).randn(2, 3, 224, 224).astype(np.float32)
    onnx_out = sess.run(["embedding"], {"pixel_values": x})[0]
    with torch.inference_mode():
        ref = model(torch.from_numpy(x)).float().numpy()
    if onnx_out.shape != ref.shape:
        raise SystemExit(f"ФОРМА НЕ СОВПАЛА: onnx {onnx_out.shape} vs timm {ref.shape}")
    diff = float(np.abs(onnx_out - ref).max())
    print(f"[verify dinov2] onnx {onnx_out.shape} vs timm {ref.shape}; max|diff| = {diff:.3e}")
    if diff > 1e-3:
        raise SystemExit("РАСХОЖДЕНИЕ > 1e-3")


def main() -> None:
    out_dir = Path(os.environ.get("REID_MODELS_DIR", DEFAULT_OUT_DIR))
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / ARTIFACT_NAME
    export(out_path)
    verify(out_path)
    print(f"[dinov2] {out_path} ({out_path.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
