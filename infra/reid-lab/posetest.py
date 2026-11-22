"""Draw ViTPose+ keypoints (dataset_index given) on a few body boxes to check what they mean."""
import json
import sys
from pathlib import Path

import torch
from PIL import Image, ImageDraw
from transformers import AutoProcessor, VitPoseForPoseEstimation

D = Path(__file__).parent
DS = int(sys.argv[1]) if len(sys.argv) > 1 else 3
proc = AutoProcessor.from_pretrained("usyd-community/vitpose-plus-base")
model = VitPoseForPoseEstimation.from_pretrained("usyd-community/vitpose-plus-base").eval()
recs = [r for r in map(json.loads, open(D / "det.jsonl")) if r["body"]][:12]
T = 480
sheet = Image.new("RGB", (T * 4, T * 3), "black")
COL = ["red", "orange", "yellow", "lime", "cyan", "blue", "magenta", "white", "pink", "brown", "gray",
       "purple", "olive", "navy", "teal", "maroon", "gold"]
for k, r in enumerate(recs):
    img = Image.open(D / "img" / f"{int(r['id']):05d}.jpg")
    x0, y0, x1, y1 = r["body"]
    inputs = proc(img, boxes=[[[x0, y0, x1 - x0, y1 - y0]]], return_tensors="pt")
    with torch.no_grad():
        out = model(**inputs, dataset_index=torch.tensor([DS]))
    res = proc.post_process_pose_estimation(out, boxes=[[[x0, y0, x1 - x0, y1 - y0]]])[0][0]
    d = ImageDraw.Draw(img)
    for j, ((x, y), s) in enumerate(zip(res["keypoints"].tolist(), res["scores"].tolist())):
        if s > 0.2:
            d.ellipse([x - 9, y - 9, x + 9, y + 9], fill=COL[j % 17])
            d.text((x + 10, y - 10), str(j), fill="white")
    s = T / max(img.size)
    img = img.resize((int(img.width * s), int(img.height * s)))
    sheet.paste(img, ((k % 4) * T, (k // 4) * T))
sheet.save(D / "posesheet.jpg", quality=85)
print("ok")
