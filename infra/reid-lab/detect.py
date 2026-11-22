"""Find the main horse, its keypoints, head crop and visible side for every photo.

body: YOLO11m COCO class "horse", largest box.
keypoints: ViTPose+ (AP-10K expert, dataset_index=3) inside the body box.
           Verified visually: 0 nose, 1-2 eyes, 3-4 ears/poll, 11-12 hips.
view: horse facing image-left shows its LEFT side, facing image-right its RIGHT side;
      nose roughly above the hips = front/rear.

Writes det.jsonl and crops/body/<id>.jpg, crops/head/<id>.jpg.
Usage: detect.py            (all photos, resumes)
       detect.py N          (every k-th photo, ~N in total, overwrites det.jsonl)
"""
import csv
import json
import os
import sys
from pathlib import Path

import torch
from PIL import Image
from transformers import AutoProcessor, VitPoseForPoseEstimation
from ultralytics import YOLO

torch.set_num_threads(int(os.environ.get("THREADS", 8)))
D = Path(__file__).parent
HORSE = 17
HEAD_KP = [0, 1, 2, 3, 4]
HIP_KP = [11, 12]
TH = 0.3


def clip_box(b, W, H):
    return [max(0, b[0]), max(0, b[1]), min(W, b[2]), min(H, b[3])]


def view_and_head(body, kps, scores):
    bx0, _, bx1, _ = body
    bw = bx1 - bx0
    ok = [i for i in range(len(scores)) if scores[i] > TH]
    nose = kps[0] if 0 in ok else None
    hips = [kps[i] for i in HIP_KP if i in ok]
    view = "unknown"
    if nose is not None and hips:
        dx = (nose[0] - sum(h[0] for h in hips) / len(hips)) / bw
        view = "left" if dx < -0.25 else "right" if dx > 0.25 else "front"
    elif nose is None and hips:
        view = "rear"
    head = None
    pts = [kps[i] for i in HEAD_KP if i in ok]
    if nose is not None and (1 in ok or 2 in ok) and len(pts) >= 3:
        xs, ys = [p[0] for p in pts], [p[1] for p in pts]
        cx, cy = (min(xs) + max(xs)) / 2, (min(ys) + max(ys)) / 2
        side = max(max(xs) - min(xs), max(ys) - min(ys)) * 1.6
        head = [cx - side / 2, cy - side / 2, cx + side / 2, cy + side / 2]
    return view, head


def main():
    rows = list(csv.DictReader(open(D / "meta.csv", encoding="utf-8")))
    sample = len(sys.argv) > 1
    if sample:
        rows = rows[:: max(1, len(rows) // int(sys.argv[1]))]
    for sub in ("body", "head"):
        (D / "crops" / sub).mkdir(parents=True, exist_ok=True)
    det = YOLO("yolo11s.pt")
    proc = AutoProcessor.from_pretrained("usyd-community/vitpose-plus-base")
    pose = VitPoseForPoseEstimation.from_pretrained("usyd-community/vitpose-plus-base").eval()
    # DET_FROM/DET_TO/DET_OUT split the bank between machines (id range, output file).
    lo, hi = int(os.environ.get("DET_FROM", 0)), int(os.environ.get("DET_TO", 10**9))
    rows = [r for r in rows if lo <= int(r["id"]) < hi]
    out_path = D / os.environ.get("DET_OUT", "det.jsonl")
    done = set() if sample or not out_path.exists() else {json.loads(l)["id"] for l in open(out_path)}
    out = open(out_path, "a" if done else "w")
    for n, r in enumerate(rows):
        if r["id"] in done:
            continue
        img = Image.open(D / "img" / f"{int(r['id']):05d}.jpg")
        W, H = img.size
        boxes = det(img, classes=[HORSE], conf=0.25, verbose=False, imgsz=640)[0].boxes.xyxy.tolist()
        rec = {"id": r["id"], "n_horses": len(boxes), "body": None, "head": None, "view": "unknown"}
        if boxes:
            body = max(boxes, key=lambda b: (b[2] - b[0]) * (b[3] - b[1]))
            x0, y0, x1, y1 = body
            rec["body"] = [round(v, 1) for v in body]
            rec["body_frac"] = round((x1 - x0) * (y1 - y0) / (W * H), 3)
            img.crop(tuple(map(int, clip_box([x0 - 8, y0 - 8, x1 + 8, y1 + 8], W, H)))).save(
                D / "crops/body" / f"{r['id']}.jpg", quality=92)
            coco = [[[x0, y0, x1 - x0, y1 - y0]]]
            with torch.no_grad():
                o = pose(**proc(img, boxes=coco, return_tensors="pt"), dataset_index=torch.tensor([3]))
            p = proc.post_process_pose_estimation(o, boxes=coco)[0][0]
            kps, scores = p["keypoints"].tolist(), p["scores"].tolist()
            rec["kps"] = [[round(x, 1), round(y, 1), round(s, 3)] for (x, y), s in zip(kps, scores)]
            rec["view"], head = view_and_head(body, kps, scores)
            if head and head[2] - head[0] >= 48:
                head = clip_box(head, W, H)
                rec["head"] = [round(v, 1) for v in head]
                img.crop(tuple(map(int, head))).save(D / "crops/head" / f"{r['id']}.jpg", quality=92)
        out.write(json.dumps(rec) + "\n")
        out.flush()
        if n % 100 == 0:
            print(n, rec["id"], rec["view"], rec["head"] is not None, flush=True)


if __name__ == "__main__":
    main()
