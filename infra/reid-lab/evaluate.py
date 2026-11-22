"""Leave-date-out re-identification benchmark: full / body / head / fusion, side matching, season & age.

For every query photo the gallery is every photo of the bank shot on ANOTHER day
(mode "other-days") or strictly BEFORE the query day (mode "past", the realistic one).
Horse score = max over that horse's gallery photos of the adjusted similarity.

Usage: evaluate.py <model> [mode]
"""
import csv
import json
import sys
from collections import defaultdict
from datetime import date
from pathlib import Path

import numpy as np

D = Path(__file__).parent
MODEL = sys.argv[1]
MODE = sys.argv[2] if len(sys.argv) > 2 else "other-days"

meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
det = {r["id"]: r for r in map(json.loads, open(D / "det.jsonl"))}
E = np.load(D / f"emb_{MODEL}.npz")
ids = [str(i) for i in E["ids"]]
keep = [k for k, i in enumerate(ids) if meta[i]["date"]]
ids = [ids[k] for k in keep]
F = {p: E[p][keep] for p in ("full", "body", "head") if p in E.files}
horse = np.array([meta[i]["horse"] for i in ids])
days = np.array([date.fromisoformat(meta[i]["date"]).toordinal() for i in ids])
month = np.array([int(meta[i]["date"][5:7]) for i in ids])
SEASON = {12: "winter", 1: "winter", 2: "winter", 3: "spring", 4: "spring", 5: "spring",
          6: "summer", 7: "summer", 8: "summer", 9: "autumn", 10: "autumn", 11: "autumn"}
season = np.array([SEASON[m] for m in month])
view = np.array([det[i]["view"] for i in ids])
horses = sorted(set(horse))
H = np.array([[h == x for x in horse] for h in horses])  # horses x photos

S = {}
for p, X in F.items():
    ok = np.isfinite(X[:, 0])
    Xz = np.nan_to_num(X)
    s = Xz @ Xz.T
    s[~ok] = np.nan
    s[:, ~ok] = np.nan
    S[p] = s
same_day = days[:, None] == days[None, :]
gallery_ok = ~same_day if MODE == "other-days" else days[None, :] < days[:, None]
years_apart = np.abs(days[:, None] - days[None, :]) / 365.25
same_season = season[:, None] == season[None, :]
side = np.isin(view, ["left", "right"])
opposite = side[:, None] & side[None, :] & (view[:, None] != view[None, :])


def horse_scores(sim, cfg):
    """sim: photos x photos (adjusted). Returns queries x horses (max over gallery photos)."""
    s = np.where(gallery_ok, sim, np.nan)
    out = np.full((len(ids), len(horses)), np.nan)
    for k in range(len(horses)):
        sub = s[:, H[k]]
        good = np.isfinite(sub).any(1)
        out[good, k] = np.nanmax(sub[good], 1)
    return out


def adjust(p, cfg):
    s = S[p].copy()
    if cfg.get("side") and p in ("body", "full"):
        s[opposite] = np.nan  # never compare left flank with right flank
    s = s + cfg.get("season", 0) * same_season - cfg.get("age", 0) * np.minimum(years_apart, 5)
    return s


def scores(cfg):
    parts = cfg["parts"]
    per = {p: horse_scores(adjust(p, cfg), cfg) for p in parts}
    if len(parts) == 1:
        return per[parts[0]]
    w = cfg.get("w_head", 0.5)
    h, b = per["head"], per["body"]
    fused = w * h + (1 - w) * b
    fused = np.where(np.isnan(h), b, fused)
    return np.where(np.isnan(b), h, fused)


def metrics(sc, mask):
    res = {}
    valid = mask & np.isfinite(sc).any(1)
    true_col = np.array([horses.index(h) for h in horse])
    sc = np.nan_to_num(sc, nan=-9)
    rank = (sc > sc[np.arange(len(ids)), true_col][:, None]).sum(1)
    for k in (1, 3, 5):
        res[f"top{k}"] = float((rank[valid] < k).mean()) if valid.any() else float("nan")
    res["n"] = int(valid.sum())
    return res


has_gallery = np.array([(gallery_ok[q] & (horse == horse[q])).any() for q in range(len(ids))])
has_head = np.isfinite(F["head"][:, 0])
CONFIGS = [
    ("full photo (baseline)", {"parts": ["full"]}),
    ("body only", {"parts": ["body"]}),
    ("head only", {"parts": ["head"]}),
    ("head + body", {"parts": ["head", "body"]}),
    ("head + body, same side", {"parts": ["head", "body"], "side": True}),
    ("+ season bonus", {"parts": ["head", "body"], "side": True, "season": 0.03}),
    ("+ season + age penalty", {"parts": ["head", "body"], "side": True, "season": 0.03, "age": 0.01}),
]
rows = []
for name, cfg in CONFIGS:
    if not all(p in F for p in cfg["parts"]):
        continue
    sc = scores(cfg)
    m = metrics(sc, has_gallery)
    mh = metrics(sc, has_gallery & has_head)
    by_season = {s: metrics(sc, has_gallery & (season == s))["top1"] for s in ("winter", "spring", "summer", "autumn")}
    rows.append({"config": name, **m, "top1_with_head": mh["top1"], **{f"top1_{k}": v for k, v in by_season.items()}})

print(f"model={MODEL} mode={MODE} queries={int(has_gallery.sum())} horses={len(horses)} "
      f"head_found={int(has_head.sum())}/{len(ids)} views={dict(zip(*np.unique(view, return_counts=True)))}")
cols = ["config", "top1", "top3", "top5", "top1_with_head", "top1_winter", "top1_spring", "top1_summer", "top1_autumn"]
print(" | ".join(cols))
for r in rows:
    print(" | ".join(r[c] if c == "config" else f"{r[c]:.3f}" for c in cols))
json.dump(rows, open(D / f"results_{MODEL}_{MODE}.json", "w"), indent=1, ensure_ascii=False)

if "--grid" in sys.argv:
    print("\ngrid (head+body, same side): w_head, season, age -> top1 / top3")
    for w in (0.3, 0.5, 0.7):
        for se in (0, 0.02, 0.05):
            for ag in (0, 0.01, 0.03):
                m = metrics(scores({"parts": ["head", "body"], "side": True, "w_head": w, "season": se, "age": ag}), has_gallery)
                print(f"{w} {se} {ag} -> {m['top1']:.3f} / {m['top3']:.3f}")
