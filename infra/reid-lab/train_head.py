"""Honest re-ID benchmark with a trained embedding head.

Split: StratifiedGroupKFold(5) — stratified by horse, grouped by shooting session (horse + date),
so frames of one session never end up both in train (gallery) and test (queries).
Per fold and per part (head / body / full) we compare:
  raw    — cosine on frozen backbone features
  arc    — features -> Linear(d, 256) trained with ArcFace on the train fold, class-balanced
Retrieval: horse score = max similarity over that horse's gallery photos (train fold);
body photos are only compared with the same visible side (left/right) when --side.
Fusion: w*head + (1-w)*body, falling back to whichever part exists.
Metrics pooled over folds: top-1, top-3, macro top-1 (mean of per-horse top-1), fold std of top-1.

Usage: train_head.py <model> [--side] [--season 0.03] [--age 0.01] [--epochs 80]
"""
import argparse
import csv
import json
from datetime import date
from pathlib import Path

import numpy as np
import torch
import torch.nn.functional as Fn
from sklearn.model_selection import StratifiedGroupKFold

D = Path(__file__).parent
ap = argparse.ArgumentParser()
ap.add_argument("model")
ap.add_argument("--side", action="store_true")
ap.add_argument("--season", type=float, default=0.0)
ap.add_argument("--age", type=float, default=0.0)
ap.add_argument("--epochs", type=int, default=80)
ap.add_argument("--dim", type=int, default=256)
ap.add_argument("--margin", type=float, default=0.3)
ap.add_argument("--w_head", type=float, default=0.5)
ap.add_argument("--folds", type=int, default=5)
ap.add_argument("--trip_days", type=int, default=0, help="merge same-horse sessions closer than N days into one group")
args = ap.parse_args()
torch.manual_seed(0)
np.random.seed(0)

meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
det = {r["id"]: r for r in map(json.loads, open(D / "det.jsonl"))}
E = np.load(D / f"emb_{args.model}.npz")
all_ids = [str(i) for i in E["ids"]]
keep = [k for k, i in enumerate(all_ids) if meta[i]["date"]]
ids = [all_ids[k] for k in keep]
PARTS = [p for p in ("head", "body", "full") if p in E.files]
F = {p: E[p][keep] for p in PARTS}
horse = np.array([meta[i]["horse"] for i in ids])
horses = sorted(set(horse))
y = np.array([horses.index(h) for h in horse])
dstr = np.array([meta[i]["date"] for i in ids])
days = np.array([date.fromisoformat(d).toordinal() for d in dstr])
SEASON = {12: 0, 1: 0, 2: 0, 3: 1, 4: 1, 5: 1, 6: 2, 7: 2, 8: 2, 9: 3, 10: 3, 11: 3}
season = np.array([SEASON[int(d[5:7])] for d in dstr])
view = np.array([det[i]["view"] for i in ids])
groups = np.array([f"{h}|{d}" for h, d in zip(horse, dstr)])
if args.trip_days:
    # merge a horse's shooting days that are <= trip_days apart into one group ("trip"),
    # so near-identical consecutive days never straddle train and test
    trip = {}
    for h in horses:
        ds = sorted(set(days[horse == h]))
        k = 0
        for a, b in zip([None] + ds[:-1], ds):
            if a is not None and b - a > args.trip_days:
                k += 1
            trip[(h, b)] = f"{h}|trip{k}"
    groups = np.array([trip[(h, d)] for h, d in zip(horse, days)])


def train_arc(X, yy):
    """Linear projection trained with class-balanced ArcFace on frozen features."""
    X = torch.tensor(X)
    yy = torch.tensor(yy)
    d = X.shape[1]
    proj = torch.nn.Sequential(torch.nn.Dropout(0.2), torch.nn.Linear(d, args.dim))
    W = torch.nn.Parameter(torch.randn(len(horses), args.dim) * 0.01)
    opt = torch.optim.AdamW(list(proj.parameters()) + [W], lr=2e-3, weight_decay=1e-3)
    counts = torch.bincount(yy, minlength=len(horses)).float().clamp(min=1)
    cls_w = (1 / counts) / (1 / counts).mean()
    s, m = 30.0, args.margin
    for ep in range(args.epochs):
        perm = torch.randperm(len(X))
        for b in range(0, len(X), 128):
            idx = perm[b:b + 128]
            xb = X[idx] + 0.02 * torch.randn_like(X[idx])
            emb = Fn.normalize(proj(xb), dim=-1)
            cos = emb @ Fn.normalize(W, dim=-1).T
            theta = torch.acos(cos.clamp(-1 + 1e-6, 1 - 1e-6))
            onehot = Fn.one_hot(yy[idx], len(horses)).bool()
            logits = s * torch.where(onehot, torch.cos(theta + m), cos)
            loss = Fn.cross_entropy(logits, yy[idx], weight=cls_w)
            opt.zero_grad()
            loss.backward()
            opt.step()
    proj.eval()
    return lambda Z: Fn.normalize(proj(torch.tensor(Z)), dim=-1).detach().numpy()


def horse_scores(sim, q, g, part):
    """sim: len(q) x len(g). Returns len(q) x horses, NaN where no usable gallery photo."""
    s = sim.copy()
    if args.side and part in ("body", "full"):
        side_q = np.isin(view[q], ["left", "right"])
        side_g = np.isin(view[g], ["left", "right"])
        opp = side_q[:, None] & side_g[None, :] & (view[q][:, None] != view[g][None, :])
        s[opp] = np.nan
    s += args.season * (season[q][:, None] == season[g][None, :])
    s -= args.age * np.minimum(np.abs(days[q][:, None] - days[g][None, :]) / 365.25, 5)
    out = np.full((len(q), len(horses)), np.nan)
    for k in range(len(horses)):
        sub = s[:, y[g] == k]
        if sub.shape[1] == 0:
            continue
        ok = np.isfinite(sub).any(1)
        out[ok, k] = np.nanmax(sub[ok], 1)
    return out


def fuse(h, b, w):
    f = w * h + (1 - w) * b
    f = np.where(np.isnan(h), b, f)
    return np.where(np.isnan(b), h, f)


results = {}  # (variant, part) -> list of (query index, score row)
sgkf = StratifiedGroupKFold(n_splits=args.folds, shuffle=True, random_state=0)
fold_of = np.zeros(len(ids), int)
for fold, (tr, te) in enumerate(sgkf.split(np.zeros(len(ids)), y, groups)):
    fold_of[te] = fold
    assert not set(groups[tr]) & set(groups[te])
    per = {}
    for p in PARTS:
        ok = np.isfinite(F[p][:, 0])
        g, q = tr[ok[tr]], te[ok[te]]
        raw = F[p]
        per[("raw", p)] = (q, horse_scores(raw[q] @ raw[g].T, q, g, p))
        enc = train_arc(raw[g], y[g])
        eg, eq = enc(raw[g]), enc(raw[q])
        per[("arc", p)] = (q, horse_scores(eq @ eg.T, q, g, p))
    for v in ("raw", "arc"):
        for p in PARTS:
            results.setdefault((v, p), []).append(per[(v, p)])
        if "head" in PARTS and "body" in PARTS:
            full_rows = {}
            for p in ("head", "body"):
                q, sc = per[(v, p)]
                mat = np.full((len(te), len(horses)), np.nan)
                pos = {i: k for k, i in enumerate(te)}
                mat[[pos[i] for i in q]] = sc
                full_rows[p] = mat
            results.setdefault((v, "head+body"), []).append((te, fuse(full_rows["head"], full_rows["body"], args.w_head)))
    print(f"fold {fold}: train {len(tr)} test {len(te)} sessions test {len(set(groups[te]))}", flush=True)


def report(chunks):
    q = np.concatenate([c[0] for c in chunks])
    sc = np.concatenate([c[1] for c in chunks])
    valid = np.isfinite(sc).any(1) & np.isfinite(sc[np.arange(len(q)), y[q]])
    q, sc = q[valid], np.nan_to_num(sc[valid], nan=-9)
    rank = (sc > sc[np.arange(len(q)), y[q]][:, None]).sum(1)
    top1 = rank < 1
    per_horse = [top1[y[q] == k].mean() for k in range(len(horses)) if (y[q] == k).any()]
    per_fold = [top1[fold_of[q] == f].mean() for f in range(args.folds) if (fold_of[q] == f).any()]
    return {"n": int(len(q)), "top1": top1.mean(), "top3": (rank < 3).mean(), "macro_top1": float(np.mean(per_horse)),
            "fold_std": float(np.std(per_fold)),
            "per_horse": {horses[k]: round(float(top1[y[q] == k].mean()), 3) for k in range(len(horses)) if (y[q] == k).any()},
            "per_season": {n: round(float(top1[season[q] == s].mean()), 3) for s, n in enumerate(["winter", "spring", "summer", "autumn"]) if (season[q] == s).any()}}


print(f"\nmodel={args.model} side={args.side} season={args.season} age={args.age} photos={len(ids)} "
      f"sessions={len(set(groups))} horses={len(horses)}")
print(f"{'variant':6} {'part':10} {'n':>5} {'top1':>6} {'top3':>6} {'macro1':>7} {'±fold':>6}  per-season top1")
out = {}
for (v, p), chunks in sorted(results.items(), key=lambda kv: (kv[0][1], kv[0][0])):
    r = report(chunks)
    out[f"{v}/{p}"] = r
    print(f"{v:6} {p:10} {r['n']:5d} {r['top1']:6.3f} {r['top3']:6.3f} {r['macro_top1']:7.3f} {r['fold_std']:6.3f}  {r['per_season']}")
tag = f"{args.model}{'_side' if args.side else ''}{'_time' if args.season or args.age else ''}"
json.dump(out, open(D / f"cv_{tag}.json", "w", encoding="utf-8"), indent=1, ensure_ascii=False, default=float)
