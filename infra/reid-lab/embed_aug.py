"""Season-robustness augmentations of head/body crops, embedded with MiewID.

aug 0 "gray"   : grayscale (deterministic)           -> ALL split photos (also used as a test-time view)
aug 1 "jitter" : strong colour jitter                -> train/val only
aug 2 "winter" : brighter, desaturated, softer       -> train/val only
aug 3 "crop"   : random resized crop + rotate + blur -> train/val only
Writes emb_miewid_aug.npz: ids, head[4,N,2152], body[4,N,2152] (NaN where not computed).
"""
import json
import os
import random
import sys
import time
from pathlib import Path

import numpy as np
import torch
import torchvision.transforms as T
from PIL import Image, ImageEnhance, ImageFilter

sys.argv = [sys.argv[0], "miewid"]
torch.set_num_threads(int(os.environ.get("THREADS", 12)))
D = Path(__file__).parent
src = (D / "embed_local.py").read_text(encoding="utf-8").split("def run(")[0]
exec(compile(src, "embed_local.py", "exec"))  # builds `model` (MiewID) and `tf`

random.seed(0)
torch.manual_seed(0)
split = json.load(open(D / "split.json", encoding="utf-8"))
ids = sorted(split, key=int)
jitter = T.ColorJitter(0.4, 0.4, 0.4, 0.05)
crop = T.Compose([T.RandomResizedCrop(440, scale=(0.65, 1.0), ratio=(0.75, 1.33)), T.RandomRotation(8)])


def winter(im):
    im = ImageEnhance.Color(im).enhance(random.uniform(0.4, 0.7))
    im = ImageEnhance.Brightness(im).enhance(random.uniform(1.1, 1.35))
    return im.filter(ImageFilter.GaussianBlur(random.uniform(0.5, 1.2)))


AUGS = [lambda im: im.convert("L").convert("RGB"), jitter, winter,
        lambda im: crop(im).filter(ImageFilter.GaussianBlur(random.uniform(0, 1.0)))]
out = {"ids": np.array(ids)}
t0 = time.time()
for part in ("head", "body"):
    arr = np.full((len(AUGS), len(ids), 2152), np.nan, dtype=np.float32)
    for a, fn in enumerate(AUGS):
        todo = [k for k, i in enumerate(ids) if (D / "crops" / part / f"{i}.jpg").exists() and (a == 0 or split[i] != "test")]
        for s in range(0, len(todo), 8):
            chunk = todo[s:s + 8]
            x = torch.stack([tf(fn(Image.open(D / "crops" / part / f"{ids[k]}.jpg").convert("RGB"))) for k in chunk])
            with torch.inference_mode():
                f = torch.nn.functional.normalize(model(x).float(), dim=-1).numpy()
            arr[a, chunk] = f
        print(f"{part} aug{a} {len(todo)} done, {time.time() - t0:.0f}s", flush=True)
    out[part] = arr
np.savez(D / "emb_miewid_aug.npz", **out)
print("saved")
