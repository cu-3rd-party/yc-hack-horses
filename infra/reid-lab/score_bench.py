"""Score the blind 30-query benchmark: VLM answers and embedding models on the same queries.

Usage: score_bench.py <answer_key.json> [vlm_answers.json] [--models miewid,dinov2]
Embeddings: body-crop cosine, horse score = max over gallery photos.
  'sheet'  — gallery = exactly the 6 photos per horse the VLM saw
  'all'    — gallery = every earlier-dated body crop of that horse
"""
import csv
import json
import sys
from pathlib import Path

import numpy as np

D = Path(__file__).parent
K = json.load(open(sys.argv[1], encoding="utf-8"))
key, sheet = K["key"], K["gallery"]
horses = sorted(sheet)
meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}


def summarize(name, preds):
    t1 = np.mean([preds[q][0] == key[q]["horse"] for q in key])
    t3 = np.mean([key[q]["horse"] in preds[q][:3] for q in key])
    print(f"{name:28} top1 {t1:.3f}  top3 {t3:.3f}  (n={len(key)})")
    return t1, t3


rows = {}
if len(sys.argv) > 2 and not sys.argv[2].startswith("--"):
    ans = json.load(open(sys.argv[2], encoding="utf-8"))
    rows["Sonnet (agent)"] = summarize("Sonnet agent", {q: ans[q]["top3"] for q in key})

models = sys.argv[sys.argv.index("--models") + 1].split(",") if "--models" in sys.argv else []
for m in models:
    f = D / f"emb_{m}.npz"
    if not f.exists():
        continue
    E = np.load(f)
    idx = {str(i): k for k, i in enumerate(E["ids"])}
    X = E["body"]
    for mode in ("sheet", "all"):
        preds = {}
        for q, v in key.items():
            x = X[idx[v["id"]]]
            sc = {}
            for h in horses:
                if mode == "sheet":
                    g = sheet[h]
                else:
                    g = [i for i, r in meta.items() if r["horse"] == h and r["date"] and r["date"] < meta[v["id"]]["date"]]
                sims = [float(X[idx[i]] @ x) for i in g if i in idx and np.isfinite(X[idx[i], 0])]
                sc[h] = max(sims) if sims else -9
            preds[q] = sorted(horses, key=lambda h: -sc[h])
        rows[f"{m} raw/{mode}"] = summarize(f"{m} raw, gallery={mode}", preds)
