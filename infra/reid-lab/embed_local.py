"""Embed body/head crops (and optionally the full photo) with extra re-ID backbones on the local CPU.

Usage: embed_local.py <model> [--full]
  model in: miewid | mega-l | dinov3
Writes emb_<model>.npz with ids and body/head (+full) matrices, NaN rows where a crop is missing.
"""
import json
import sys
import time
from pathlib import Path

import numpy as np
import torch
import torchvision.transforms as T
from PIL import Image

torch.set_num_threads(int(__import__("os").environ.get("THREADS", "14")))
D = Path(__file__).parent
NAME = sys.argv[1]
IMNET = ((0.485, 0.456, 0.406), (0.229, 0.224, 0.225))

if NAME == "miewid":
    # Rebuilt from the repo's modeling_miewid.py (its remote code breaks on transformers 5):
    # efficientnetv2_rw_m backbone + GeM pooling + BatchNorm1d(2152); the ArcFace head is not needed.
    import timm
    from huggingface_hub import hf_hub_download
    from safetensors.torch import load_file

    class GeM(torch.nn.Module):
        def __init__(self):
            super().__init__()
            self.p = torch.nn.Parameter(torch.ones(1) * 3)

        def forward(self, x):
            return torch.nn.functional.avg_pool2d(x.clamp(min=1e-6).pow(self.p), x.shape[-2:]).pow(1.0 / self.p)

    class MiewID(torch.nn.Module):
        def __init__(self):
            super().__init__()
            self.backbone = timm.create_model("efficientnetv2_rw_m", pretrained=False, num_classes=0)
            self.backbone.global_pool = GeM()
            self.bn = torch.nn.BatchNorm1d(2152)

        def forward(self, x):
            return self.bn(self.backbone(x).flatten(1))

    model = MiewID()
    sd = load_file(hf_hub_download("conservationxlabs/miewid-msv3", "model.safetensors"))
    missing, unexpected = model.load_state_dict({k: v for k, v in sd.items() if not k.startswith("final.")}, strict=False)
    assert not missing, missing
    model.eval()
    size, (mean, std) = 440, IMNET
elif NAME == "mega-l":
    import timm
    model = timm.create_model("hf-hub:BVRA/MegaDescriptor-L-384", pretrained=True, num_classes=0).eval()
    size, mean, std = 384, (0.5, 0.5, 0.5), (0.5, 0.5, 0.5)
elif NAME == "dinov3":
    import timm
    model = timm.create_model("vit_base_patch16_dinov3.lvd1689m", pretrained=True, num_classes=0).eval()
    size, (mean, std) = 256, IMNET
else:
    raise SystemExit("unknown model " + NAME)
tf = T.Compose([T.Resize((size, size), interpolation=T.InterpolationMode.BICUBIC), T.ToTensor(), T.Normalize(mean, std)])


def run(paths, tag):
    feats = None
    idx = [i for i, p in enumerate(paths) if p.exists()]
    t0 = time.time()
    for s in range(0, len(idx), 8):
        chunk = idx[s:s + 8]
        x = torch.stack([tf(Image.open(paths[i]).convert("RGB")) for i in chunk])
        with torch.inference_mode():
            f = model(x)
        f = torch.nn.functional.normalize(f.float(), dim=-1).numpy()
        if feats is None:
            feats = np.full((len(paths), f.shape[1]), np.nan, dtype=np.float32)
        feats[chunk] = f
        if s % 200 == 0:
            el = time.time() - t0
            print(f"{tag} {s}/{len(idx)} {el:.0f}s eta {el / max(1, s) * (len(idx) - s):.0f}s", flush=True)
    return feats


recs = [json.loads(l) for l in open(D / "det.jsonl")]
ids = [r["id"] for r in recs]
out = {"ids": np.array(ids)}
out["head"] = run([D / "crops/head" / f"{i}.jpg" for i in ids], "head")
out["body"] = run([D / "crops/body" / f"{i}.jpg" for i in ids], "body")
if "--full" in sys.argv:
    out["full"] = run([D / "img" / f"{int(i):05d}.jpg" for i in ids], "full")
np.savez(D / f"emb_{NAME}.npz", **out)
print("done", {k: int(np.isfinite(v[:, 0]).sum()) for k, v in out.items() if k != "ids"})
