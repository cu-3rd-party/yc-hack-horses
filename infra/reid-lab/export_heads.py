"""Export the demo's ArcFace projections (exactly as demo_app.py trains them) for porting to Go.

Writes export/heads_<bb>_<part>.f32 (row-major float32: weight[256][D] then bias[256]),
export/gallery_<bb>_<part>.f32 (N x 256 unit vectors of the bank photos), export/gallery.json, export/meta.json.
"""
import json
import sys
from pathlib import Path

import numpy as np

D = Path(__file__).parent
src = (D / "demo_app.py").read_text(encoding="utf-8").split("# ---------- models for the uploaded photo ----------")[0]
g = {"__name__": "export", "__file__": str(D / "demo_app.py")}
exec(compile(src, "demo_app.py", "exec"), g)
out = D / "export"
out.mkdir(exist_ok=True)
info = {"config": g["CFG"], "horses": g["horses"], "dim": 256, "parts": {}, "temperature": g["TEMP"]}
for (bb, part), enc in g["ENC"].items():
    lin = enc.proj[1]
    W = lin.weight.detach().numpy().astype("<f4")
    b = lin.bias.detach().numpy().astype("<f4")
    (out / f"heads_{bb}_{part}.f32").write_bytes(W.tobytes() + b.tobytes())
    M = g["GAL"][(bb, part)].astype("<f4")
    (out / f"gallery_{bb}_{part}.f32").write_bytes(M.tobytes())
    info["parts"][f"{bb}/{part}"] = {"in_dim": int(W.shape[1]), "out_dim": int(W.shape[0]), "gallery_rows": int(M.shape[0])}
json.dump([{"photo_id": i, "horse": g["meta"][i]["horse"], "date": g["meta"][i]["date"], "view": g["det"][i]["view"]}
           for i in g["gids"]], open(out / "gallery.json", "w", encoding="utf-8"), ensure_ascii=False)
json.dump(info, open(out / "meta.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print("exported", info["parts"])
