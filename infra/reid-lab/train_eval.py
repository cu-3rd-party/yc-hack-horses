"""Tune on VAL only, retrain on TRAIN+VAL, evaluate ONCE on the held-out TEST (split.json from split.py).

Knobs searched on val: backbone (miewid | dinov2 | ensemble), season augmentations for the ArcFace head,
grayscale test-time view, head weight, per-horse aggregation (max | mean of top-2).
Reports on test: chosen config, plus reference rows (raw embeddings, previous default) that are NOT used for selection.
Metrics: top-1, top-3, macro top-1 (mean over horses), top-1 per season, and "cross-season" top-1 =
queries whose season never occurs in that horse's training photos.

Usage: train_eval.py [--with-old]   (--with-old: extra reference row that also uses pre-cutoff photos for train/gallery)
"""
import csv
import itertools
import json
import sys
from collections import defaultdict
from pathlib import Path

import numpy as np
import torch
import torch.nn.functional as Fn

D = Path(__file__).parent
SEASON = {12: "winter", 1: "winter", 2: "winter", 3: "spring", 4: "spring", 5: "spring",
          6: "summer", 7: "summer", 8: "summer", 9: "autumn", 10: "autumn", 11: "autumn"}
meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
split = json.load(open(D / "split.json", encoding="utf-8"))
horses = sorted({meta[i]["horse"] for i in split})
season = {i: SEASON[int(r["date"][5:7])] if r["date"] else None for i, r in meta.items()}


def load(name):
    E = np.load(D / f"emb_{name}.npz")
    row = {str(i): k for k, i in enumerate(E["ids"])}
    return {p: E[p] for p in ("head", "body")}, row


FEAT = {"miewid": load("miewid"), "dinov2": load("dinov2")}
AUG = None
if (D / "emb_miewid_aug.npz").exists():
    A = np.load(D / "emb_miewid_aug.npz")
    AUG = ({p: A[p] for p in ("head", "body")}, {str(i): k for k, i in enumerate(A["ids"])})


def vec(bb, part, i, view="orig"):
    if view == "orig":
        F, row = FEAT[bb]
        return F[part][row[i]] if i in row else None
    F, row = AUG
    return F[part][view][row[i]] if i in row else None


def train_arc(X, y, epochs=80, dim=256, m=0.3, s=30.0):
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
    return lambda v: Fn.normalize(proj(torch.tensor(v)[None]), dim=-1)[0].detach().numpy()


_cache = {}


def encoder(bb, part, fit_ids, aug, raw):
    key = (bb, part, tuple(fit_ids), aug, raw)
    if key in _cache:
        return _cache[key]
    if raw:
        enc = lambda v: v / np.linalg.norm(v)  # noqa: E731
    else:
        X, y = [], []
        views = ["orig"] + ([0, 1, 2, 3] if aug and bb == "miewid" and AUG else [])
        for i in fit_ids:
            for v in views:
                x = vec(bb, part, i, v)
                if x is not None and np.isfinite(x[0]):
                    X.append(x)
                    y.append(horses.index(meta[i]["horse"]))
        enc = train_arc(X, y, epochs=max(16, round(80 / len(views))))  # same number of steps with or without augs
    _cache[key] = enc
    return enc


def embed_set(bb, part, ids_, enc, gray):
    out = {}
    for i in ids_:
        x = vec(bb, part, i)
        if x is None or not np.isfinite(x[0]):
            continue
        e = enc(x)
        if gray and bb == "miewid" and AUG:
            g = vec(bb, part, i, 0)
            if g is not None and np.isfinite(g[0]):
                e = e + enc(g)
                e = e / np.linalg.norm(e)
        out[i] = e
    return out


def horse_scores(cfg, fit_ids, query_ids):
    """returns {query_id: np.array(len(horses))}"""
    total = defaultdict(lambda: np.zeros(len(horses)))
    bbs = ["miewid", "dinov2"] if cfg["bb"] == "ensemble" else [cfg["bb"]]
    for bb in bbs:
        fused = {}
        for part in ("head", "body"):
            enc = encoder(bb, part, fit_ids, cfg["aug"], cfg.get("raw", False))
            G = embed_set(bb, part, fit_ids, enc, cfg["gray"])
            Q = embed_set(bb, part, query_ids, enc, cfg["gray"])
            gid = list(G)
            gm = np.stack([G[i] for i in gid])
            gh = np.array([horses.index(meta[i]["horse"]) for i in gid])
            for q, e in Q.items():
                s = gm @ e
                sc = np.full(len(horses), np.nan)
                for k in range(len(horses)):
                    v = np.sort(s[gh == k])[::-1]
                    if len(v):
                        sc[k] = v[0] if cfg["agg"] == "max" else v[:2].mean()
                fused.setdefault(q, {})[part] = sc
        for q, parts in fused.items():
            h, b = parts.get("head"), parts.get("body")
            if h is not None and b is not None:
                f = cfg["w"] * h + (1 - cfg["w"]) * b
            else:
                f = h if h is not None else b
            total[q] += np.nan_to_num(f, nan=-2) / len(bbs)
    return total


def metrics(scores, fit_ids):
    fit_seasons = defaultdict(set)
    for i in fit_ids:
        fit_seasons[meta[i]["horse"]].add(season[i])
    rows = []
    for q, sc in scores.items():
        t = horses.index(meta[q]["horse"])
        rank = int((sc > sc[t]).sum())
        rows.append((meta[q]["horse"], season[q], season[q] not in fit_seasons[meta[q]["horse"]], rank))
    r = np.array([x[3] for x in rows])
    per_h = defaultdict(list)
    for h, _, _, k in rows:
        per_h[h].append(k == 0)
    out = {"n": len(rows), "top1": float((r == 0).mean()), "top3": float((r < 3).mean()),
           "macro1": float(np.mean([np.mean(v) for v in per_h.values()])),
           "per_horse": {h: f"{sum(v)}/{len(v)}" for h, v in sorted(per_h.items())}}
    for s in ("winter", "spring", "summer", "autumn"):
        m = [x[3] == 0 for x in rows if x[1] == s]
        out[s] = (float(np.mean(m)), len(m)) if m else None
    cs = [x[3] == 0 for x in rows if x[2]]
    out["cross_season"] = (float(np.mean(cs)), len(cs)) if cs else None
    return out


def fmt(name, m):
    seas = " ".join(f"{s[:3]} {m[s][0]:.2f}({m[s][1]})" if m[s] else f"{s[:3]} —" for s in ("winter", "spring", "summer", "autumn"))
    cs = f"{m['cross_season'][0]:.2f}({m['cross_season'][1]})" if m["cross_season"] else "—"
    return f"{name:46} n={m['n']:3} top1 {m['top1']:.3f} top3 {m['top3']:.3f} macro1 {m['macro1']:.3f} | cross-season {cs} | {seas}"


train = [i for i, s in split.items() if s == "train"]
val = [i for i, s in split.items() if s == "val"]
test = [i for i, s in split.items() if s == "test"]

# ---------------- tuning: grouped CV inside TRAIN+VAL (test never touched) ----------------
from sklearn.model_selection import StratifiedGroupKFold  # noqa: E402

trips = json.load(open(D / "trips.json", encoding="utf-8"))
dev = train + val
folds = []
for tr, va in StratifiedGroupKFold(n_splits=4, shuffle=True, random_state=0).split(
        np.zeros(len(dev)), [meta[i]["horse"] for i in dev], [trips[i] for i in dev]):
    f_ids = [dev[k] for k in tr]
    known = {meta[i]["horse"] for i in f_ids}
    q_ids = [dev[k] for k in va if meta[dev[k]]["horse"] in known]  # a query needs its horse in the gallery
    assert not {trips[i] for i in f_ids} & {trips[i] for i in q_ids}
    folds.append((f_ids, q_ids))


def cv_metrics(cfg):
    scores, fit_union = {}, []
    per_fold = []
    for f_ids, q_ids in folds:
        s = horse_scores(cfg, f_ids, q_ids)
        per_fold.append(metrics(s, f_ids))
    n = sum(m["n"] for m in per_fold)
    out = {k: sum(m[k] * m["n"] for m in per_fold) / n for k in ("top1", "top3", "macro1")}
    out["n"] = n
    for s in ("winter", "spring", "summer", "autumn", "cross_season"):
        parts = [m[s] for m in per_fold if m[s]]
        out[s] = (sum(a * b for a, b in parts) / sum(b for _, b in parts), sum(b for _, b in parts)) if parts else None
    return out


grid = [dict(bb=bb, aug=aug, gray=gray, w=w, agg=agg)
        for bb, aug, gray, w, agg in itertools.product(["miewid", "dinov2", "ensemble"], [False, True], [False, True],
                                                       [0.3, 0.5, 0.7], ["max", "top2"])
        if not ((aug or gray) and bb == "dinov2")]
print(f"train {len(train)}  val {len(val)}  test {len(test)}  | {len(grid)} configs, tuned by 4-fold grouped CV on train+val", flush=True)
res = []
for cfg in grid:
    m = cv_metrics(cfg)
    res.append((m["macro1"] + 0.5 * m["top3"], cfg, m))
res.sort(key=lambda x: -x[0])
print("\nTop configs in dev-CV (selection score = macro top-1 + 0.5*top-3):")
for sc, cfg, m in res[:8]:
    print(fmt(json.dumps(cfg), m))
best = res[0][1]
print("\nCHOSEN:", best)
if "--dev-old" in sys.argv:
    # Decide "add pre-cutoff photos to train/gallery" WITHOUT the test set: same dev-CV folds, old photos added to each fit set.
    old = [i for i, r in meta.items() if r["date"] and r["date"] < "2024-11-22" and i not in split
           and vec("miewid", "body", i) is not None and np.isfinite(vec("miewid", "body", i)[0])]
    base_folds = list(folds)
    print(fmt("dev-CV chosen, last 2 years only", cv_metrics(best)))
    folds[:] = [(f + old, q) for f, q in base_folds]
    print(fmt(f"dev-CV chosen + {len(old)} old photos", cv_metrics(best)))
    sys.exit(0)
if "--val-only" in sys.argv:
    sys.exit(0)

# ---------------- final: retrain on TRAIN+VAL, evaluate once on TEST ----------------
fit = train + val
print("\n=== TEST (held out; evaluated once, after selection) ===")
final = metrics(horse_scores(best, fit, test), fit)
print(fmt("chosen " + json.dumps(best), final))
print("  per horse top-1:", final["per_horse"])
refs = [("ref: raw MiewID head+body, max", dict(bb="miewid", aug=False, gray=False, w=0.5, agg="max", raw=True)),
        ("ref: previous default (MiewID ArcFace, w=.5, max)", dict(bb="miewid", aug=False, gray=False, w=0.5, agg="max"))]
for name, cfg in refs:
    print(fmt(name, metrics(horse_scores(cfg, fit, test), fit)))
if "--with-old" in sys.argv:
    old = [i for i, r in meta.items() if r["date"] and r["date"] < "2024-11-22" and i not in split
           and vec("miewid", "body", i) is not None and np.isfinite(vec("miewid", "body", i)[0])]
    print(fmt(f"ref: chosen + {len(old)} pre-2024-11 photos in train/gallery", metrics(horse_scores(best, fit + old, test), fit + old)))
json.dump({"chosen": best, "test": final, "val_top": [(c, m) for _, c, m in res[:8]]},
          open(D / "final_results.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1, default=str)
