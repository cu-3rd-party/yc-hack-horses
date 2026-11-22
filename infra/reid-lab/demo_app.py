"""Local demo: upload a photo -> horse/head detection, side, % match per horse, browse each horse's photos.

Honest setup (same as train_eval.py):
- photo bank (gallery) and ArcFace training = TRAIN+VAL of split.json (last 2 years, grouped by trips);
- TEST photos are never used; they are exported to test_photos/ for manual checks ("random test photo" button);
- model config = the one chosen by grouped CV on train+val (final_results.json -> "chosen").
Run:  ..\\.venv\\Scripts\\python demo_app.py   ->  http://127.0.0.1:8790
"""
import base64
import csv
import io
import json
import os
from pathlib import Path

import numpy as np
import torch
import torch.nn.functional as Fn
import torchvision.transforms as T
import uvicorn
from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse
from PIL import Image, ImageDraw, ImageOps

torch.set_num_threads(int(os.environ.get("THREADS", 8)))
D = Path(__file__).parent
PORT = int(os.environ.get("PORT", 8790))
TEMP = 20.0  # softmax temperature for the displayed percentages (not calibrated)
SEASON = {12: "зима", 1: "зима", 2: "зима", 3: "весна", 4: "весна", 5: "весна",
          6: "лето", 7: "лето", 8: "лето", 9: "осень", 10: "осень", 11: "осень"}
VIEW_RU = {"left": "левый бок", "right": "правый бок", "front": "анфас", "rear": "сзади", "unknown": "не определён"}

meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
det = {r["id"]: r for r in map(json.loads, open(D / "det.jsonl"))}
split = json.load(open(D / "split.json", encoding="utf-8"))
CFG = {"bb": "miewid", "aug": False, "gray": False, "w": 0.5, "agg": "max"}
if (D / "final_results.json").exists():
    CFG = json.load(open(D / "final_results.json", encoding="utf-8"))["chosen"]
BBS = ["miewid", "dinov2"] if CFG["bb"] == "ensemble" else [CFG["bb"]]
gids = sorted((i for i, s in split.items() if s != "test"), key=int)
if os.environ.get("WITH_OLD", "1") == "1":  # older (pre-cutoff) photos also go to training + bank; test stays held out
    gids += sorted((i for i, r in meta.items() if r["date"] and r["date"] < "2024-11-22" and i not in split
                    and det[i]["body"]), key=int)
TEST_IDS = sorted((i for i, s in split.items() if s == "test"), key=int)
horses = sorted({meta[i]["horse"] for i in gids})
gy = np.array([horses.index(meta[i]["horse"]) for i in gids])


def load(name):
    E = np.load(D / f"emb_{name}.npz")
    row = {str(i): k for k, i in enumerate(E["ids"])}
    return {p: E[p] for p in ("head", "body")}, row


FEAT = {bb: load(bb) for bb in set(BBS) | {"miewid"}}
AUG = None
if (D / "emb_miewid_aug.npz").exists():
    A = np.load(D / "emb_miewid_aug.npz")
    AUG = ({p: A[p] for p in ("head", "body")}, {str(i): k for k, i in enumerate(A["ids"])})


def vec(bb, part, i, view="orig"):
    F, row = FEAT[bb] if view == "orig" else AUG
    if i not in row:
        return None
    x = F[part][row[i]] if view == "orig" else F[part][view][row[i]]
    return x if np.isfinite(x[0]) else None


def train_arc(X, y, epochs=80, dim=256, m=0.3, s=30.0):  # identical to train_eval.py
    torch.manual_seed(0)
    X, y = torch.tensor(np.stack(X)), torch.tensor(y)
    proj = torch.nn.Sequential(torch.nn.Dropout(0.2), torch.nn.Linear(X.shape[1], dim))
    W = torch.nn.Parameter(torch.randn(len(horses), dim) * 0.01)
    opt = torch.optim.AdamW(list(proj.parameters()) + [W], lr=2e-3, weight_decay=1e-3)
    cnt = torch.bincount(y, minlength=len(horses)).float().clamp(min=1)
    cw = (1 / cnt) / (1 / cnt).mean()
    for _ in range(epochs):
        perm = torch.randperm(len(X))
        for b in range(0, len(X), 128):
            k = perm[b:b + 128]
            e = Fn.normalize(proj(X[k] + 0.02 * torch.randn_like(X[k])), dim=-1)
            cos = e @ Fn.normalize(W, dim=-1).T
            th = torch.acos(cos.clamp(-1 + 1e-6, 1 - 1e-6))
            loss = Fn.cross_entropy(s * torch.where(Fn.one_hot(y[k], len(horses)).bool(), torch.cos(th + m), cos), y[k], weight=cw)
            opt.zero_grad()
            loss.backward()
            opt.step()
    proj.eval()
    f = lambda v: Fn.normalize(proj(torch.tensor(v, dtype=torch.float32)[None]), dim=-1)[0].detach().numpy()  # noqa: E731
    f.proj = proj
    return f


def combine(enc, x, g):
    e = enc(x)
    if g is not None:
        e = e + enc(g)
        e = e / np.linalg.norm(e)
    return e


ENC, GAL = {}, {}  # (bb, part) -> encoder ; (bb, part) -> (matrix, row index into gids or -1)
for bb in BBS:
    for part in ("head", "body"):
        X, y = [], []
        views = ["orig"] + ([0, 1, 2, 3] if CFG["aug"] and bb == "miewid" and AUG else [])
        for i in gids:
            for v in views:
                x = vec(bb, part, i, v)
                if x is not None:
                    X.append(x)
                    y.append(horses.index(meta[i]["horse"]))
        enc = train_arc(X, y, epochs=max(16, round(80 / len(views))))  # same number of steps with or without augs
        ENC[(bb, part)] = enc
        use_gray = CFG["gray"] and bb == "miewid" and AUG
        M = np.full((len(gids), 256), np.nan, dtype=np.float32)
        for k, i in enumerate(gids):
            x = vec(bb, part, i)
            if x is not None:
                M[k] = combine(enc, x, vec(bb, part, i, 0) if use_gray else None)
        GAL[(bb, part)] = M
RAW_BODY = np.stack([vec("miewid", "body", i) if vec("miewid", "body", i) is not None else np.zeros(2152) for i in gids])
print(f"config {CFG}; gallery {len(gids)} photos (train+val), {len(horses)} horses; test held out: {len(TEST_IDS)}", flush=True)

# ---------- models for the uploaded photo ----------
from transformers import AutoProcessor, VitPoseForPoseEstimation  # noqa: E402
from ultralytics import YOLO  # noqa: E402
import timm  # noqa: E402
from huggingface_hub import hf_hub_download  # noqa: E402
from safetensors.torch import load_file  # noqa: E402

yolo = YOLO(str(D / "yolo11s.pt") if (D / "yolo11s.pt").exists() else "yolo11s.pt")
vproc = AutoProcessor.from_pretrained("usyd-community/vitpose-plus-base")
vpose = VitPoseForPoseEstimation.from_pretrained("usyd-community/vitpose-plus-base").eval()
IMNET = ((0.485, 0.456, 0.406), (0.229, 0.224, 0.225))


class GeM(torch.nn.Module):
    def __init__(self):
        super().__init__()
        self.p = torch.nn.Parameter(torch.ones(1) * 3)

    def forward(self, x):
        return Fn.avg_pool2d(x.clamp(min=1e-6).pow(self.p), x.shape[-2:]).pow(1.0 / self.p)


class MiewID(torch.nn.Module):
    def __init__(self):
        super().__init__()
        self.backbone = timm.create_model("efficientnetv2_rw_m", pretrained=False, num_classes=0)
        self.backbone.global_pool = GeM()
        self.bn = torch.nn.BatchNorm1d(2152)

    def forward(self, x):
        return self.bn(self.backbone(x).flatten(1))


BACK = {}
miew = MiewID()
sd = load_file(hf_hub_download("conservationxlabs/miewid-msv3", "model.safetensors"))
miew.load_state_dict({k: v for k, v in sd.items() if not k.startswith("final.")}, strict=False)
BACK["miewid"] = (miew.eval(), T.Compose([T.Resize((440, 440), interpolation=T.InterpolationMode.BICUBIC), T.ToTensor(), T.Normalize(*IMNET)]))
if "dinov2" in BBS:
    dino = timm.create_model("vit_base_patch14_dinov2.lvd142m", pretrained=True, num_classes=0, img_size=224).eval()
    BACK["dinov2"] = (dino, T.Compose([T.Resize((224, 224), interpolation=T.InterpolationMode.BICUBIC), T.ToTensor(), T.Normalize(*IMNET)]))
print("models loaded", flush=True)


def raw_embed(bb, crop):
    """same as the offline embeddings: backbone output, L2-normalised"""
    model, tf = BACK[bb]
    with torch.inference_mode():
        return Fn.normalize(model(tf(crop.convert("RGB")).unsqueeze(0)).float(), dim=-1)[0].numpy()


def clip_box(b, W, H):
    return [max(0, b[0]), max(0, b[1]), min(W, b[2]), min(H, b[3])]


def analyse(img):
    W, H = img.size
    boxes = yolo(img, classes=[17], conf=0.25, verbose=False, imgsz=640)[0].boxes.xyxy.tolist()
    if not boxes:
        return None
    body = max(boxes, key=lambda b: (b[2] - b[0]) * (b[3] - b[1]))
    x0, y0, x1, y1 = body
    coco = [[[x0, y0, x1 - x0, y1 - y0]]]
    with torch.no_grad():
        o = vpose(**vproc(img, boxes=coco, return_tensors="pt"), dataset_index=torch.tensor([3]))
    p = vproc.post_process_pose_estimation(o, boxes=coco)[0][0]
    kps, sc = p["keypoints"].tolist(), p["scores"].tolist()
    ok = [i for i in range(len(sc)) if sc[i] > 0.3]
    view, head = "unknown", None
    hips = [kps[i] for i in (11, 12) if i in ok]
    if 0 in ok and hips:
        dx = (kps[0][0] - sum(h[0] for h in hips) / len(hips)) / (x1 - x0)
        view = "left" if dx < -0.25 else "right" if dx > 0.25 else "front"
    elif 0 not in ok and hips:
        view = "rear"
    pts = [kps[i] for i in range(5) if i in ok]
    if 0 in ok and (1 in ok or 2 in ok) and len(pts) >= 3:
        xs, ys = [q[0] for q in pts], [q[1] for q in pts]
        cx, cy = (min(xs) + max(xs)) / 2, (min(ys) + max(ys)) / 2
        side = max(max(xs) - min(xs), max(ys) - min(ys)) * 1.6
        if side >= 48:
            head = clip_box([cx - side / 2, cy - side / 2, cx + side / 2, cy + side / 2], W, H)
    body_c = img.crop(tuple(map(int, clip_box([x0 - 8, y0 - 8, x1 + 8, y1 + 8], W, H))))
    head_c = img.crop(tuple(map(int, head))) if head else None
    return {"n": len(boxes), "body": body, "head": head, "view": view, "body_crop": body_c, "head_crop": head_c}


def b64(im, max_side=900):
    im = im.copy()
    im.thumbnail((max_side, max_side))
    buf = io.BytesIO()
    im.convert("RGB").save(buf, "JPEG", quality=85)
    return "data:image/jpeg;base64," + base64.b64encode(buf.getvalue()).decode()


def photo_info(i):
    d = meta[i]["date"]
    return {"id": i, "date": d, "season": SEASON[int(d[5:7])] if d else "", "view": VIEW_RU.get(det[i]["view"], "")}


def agg(v):
    v = np.sort(v[np.isfinite(v)])[::-1]
    if not len(v):
        return np.nan
    return v[0] if CFG["agg"] == "max" else v[:2].mean()


app = FastAPI()
TEST_DIR = D / "test_photos"


@app.get("/")
def index():
    return HTMLResponse((D / "demo.html").read_text(encoding="utf-8"))


@app.get("/api/info")
def info():
    return {"config": CFG, "gallery": len(gids), "test": len(TEST_IDS), "horses": len(horses)}


@app.get("/crop/{part}/{pid}.jpg")
def crop(part: str, pid: str):
    if part not in ("body", "head") or not pid.isdigit():
        raise HTTPException(404)
    f = D / "crops" / part / f"{pid}.jpg"
    if not f.exists():
        raise HTTPException(404)
    return FileResponse(f, headers={"Cache-Control": "max-age=86400"})


@app.get("/api/test_photos")
def test_photos():
    return sorted(f.name for f in TEST_DIR.glob("*.jpg")) if TEST_DIR.exists() else []


@app.get("/test_photo/{name}")
def test_photo(name: str):
    f = TEST_DIR / name
    if "/" in name or "\\" in name or ".." in name or not f.exists():
        raise HTTPException(404)
    return FileResponse(f)


@app.get("/api/horses")
def list_horses():
    out = []
    for h in horses:
        ids = sorted([i for i in gids if meta[i]["horse"] == h], key=lambda i: meta[i]["date"], reverse=True)
        out.append({"name": h, "count": len(ids), "cover": ids[0], "photos": [photo_info(i) for i in ids]})
    return out


@app.post("/api/identify")
async def identify(file: UploadFile = File(...)):
    try:
        img = ImageOps.exif_transpose(Image.open(io.BytesIO(await file.read()))).convert("RGB")
    except Exception:
        raise HTTPException(400, "Не удалось прочитать изображение")
    img.thumbnail((1280, 1280))
    a = analyse(img)
    if a is None:
        return JSONResponse({"error": "Лошадь на фото не найдена"})
    vis = img.copy()
    dr = ImageDraw.Draw(vis)
    lw = max(3, img.width // 250)
    dr.rectangle(a["body"], outline=(190, 242, 100), width=lw)
    if a["head"]:
        dr.rectangle(a["head"], outline=(255, 120, 90), width=lw)

    crops = {"body": a["body_crop"], "head": a["head_crop"]}
    part_sims = {"head": [], "body": []}  # per backbone: sims to every gallery photo
    raw_body_q = None
    for bb in BBS:
        for part, c in crops.items():
            if c is None:
                continue
            x = raw_embed(bb, c)
            if bb == "miewid" and part == "body":
                raw_body_q = x
            g = raw_embed(bb, c.convert("L").convert("RGB")) if CFG["gray"] and bb == "miewid" and AUG else None
            e = combine(ENC[(bb, part)], x, g)
            part_sims[part].append(GAL[(bb, part)] @ e)
    sims = {p: np.mean(v, axis=0) for p, v in part_sims.items() if v}
    w = CFG["w"]
    if "head" in sims:
        per_photo = np.where(np.isnan(sims["head"]), sims["body"],
                             np.where(np.isnan(sims["body"]), sims["head"], w * sims["head"] + (1 - w) * sims["body"]))
    else:
        per_photo = sims["body"]

    duplicate = None
    if raw_body_q is not None:
        d = RAW_BODY @ raw_body_q
        k = int(np.argmax(d))
        if d[k] > 0.985:
            duplicate = photo_info(gids[k]) | {"horse": meta[gids[k]]["horse"]}

    rows = []
    for k, h in enumerate(horses):
        m = gy == k
        hs = agg(sims["head"][m]) if "head" in sims else np.nan
        bs = agg(sims["body"][m])
        score = hs if np.isnan(bs) else bs if np.isnan(hs) else w * hs + (1 - w) * bs
        idx = np.where(m)[0]
        best = idx[np.argsort(-np.nan_to_num(per_photo[idx], nan=-9))][:6]
        rows.append({"name": h, "score": float(score), "head": None if np.isnan(hs) else float(hs),
                     "body": None if np.isnan(bs) else float(bs),
                     "best": [photo_info(gids[i]) | {"sim": float(per_photo[i])} for i in best]})
    sc = np.array([r["score"] for r in rows])
    pr = np.exp(TEMP * (sc - sc.max()))
    pr /= pr.sum()
    for r, p in zip(rows, pr):
        r["pct"] = round(float(p) * 100, 1)
    rows.sort(key=lambda r: -r["pct"])
    return {"query": b64(vis), "head_crop": b64(a["head_crop"], 300) if a["head_crop"] else None,
            "view": VIEW_RU[a["view"]], "n_horses": a["n"], "head_found": a["head_crop"] is not None,
            "duplicate": duplicate, "results": rows}


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=PORT, log_level="warning")
