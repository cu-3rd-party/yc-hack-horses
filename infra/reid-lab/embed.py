"""Embed full photo, body crop and head crop with one backbone.

Usage: embed.py <model>   model in: dinov2 | mega-t | mega-l
Writes emb_<model>.npz with ids and full/body/head matrices (NaN rows where the crop is missing).
"""
import json
import sys
from pathlib import Path

import numpy as np
import timm
import torch
import torchvision.transforms as T
from PIL import Image

torch.set_num_threads(6)
D = Path(__file__).parent
NAME = sys.argv[1]
MODELS = {
    "dinov2": ("vit_base_patch14_dinov2.lvd142m", 224, (0.485, 0.456, 0.406), (0.229, 0.224, 0.225)),
    "mega-t": ("hf-hub:BVRA/MegaDescriptor-T-224", 224, (0.5, 0.5, 0.5), (0.5, 0.5, 0.5)),
    "mega-l": ("hf-hub:BVRA/MegaDescriptor-L-384", 384, (0.5, 0.5, 0.5), (0.5, 0.5, 0.5)),
}
arch, size, mean, std = MODELS[NAME]
kw = {"img_size": size} if NAME == "dinov2" else {}
model = timm.create_model(arch, pretrained=True, num_classes=0, **kw).eval()
tf = T.Compose([T.Resize((size, size), interpolation=T.InterpolationMode.BICUBIC), T.ToTensor(), T.Normalize(mean, std)])


def run(paths):
    feats = np.full((len(paths), model.num_features), np.nan, dtype=np.float32)
    idx = [i for i, p in enumerate(paths) if p.exists()]
    for s in range(0, len(idx), 16):
        chunk = idx[s:s + 16]
        x = torch.stack([tf(Image.open(paths[i]).convert("RGB")) for i in chunk])
        with torch.no_grad():
            f = torch.nn.functional.normalize(model(x), dim=-1).numpy()
        feats[chunk] = f
        if s % 320 == 0:
            print(s, "/", len(idx), flush=True)
    return feats


recs = [json.loads(l) for l in open(D / "det.jsonl")]
ids = [r["id"] for r in recs]
out = {"ids": np.array(ids)}
out["full"] = run([D / "img" / f"{int(i):05d}.jpg" for i in ids])
out["body"] = run([D / "crops/body" / f"{i}.jpg" for i in ids])
out["head"] = run([D / "crops/head" / f"{i}.jpg" for i in ids])
np.savez(D / f"emb_{NAME}.npz", **out)
print("done", {k: int(np.isfinite(v[:, 0]).sum()) for k, v in out.items() if k != "ids"})
