"""Blind benchmark for a VLM agent.

queries: 2 body crops per horse from that horse's LATEST shooting day -> vlm_bench/queries/qNN.jpg
         (re-encoded, no EXIF, shuffled; answer key written OUTSIDE the bench folder).
gallery: up to 6 body crops per horse from EARLIER days, chosen for diversity (left/right, seasons),
         one labelled contact sheet per horse -> vlm_bench/gallery/<horse>.jpg
Usage: make_vlm_bench.py <answer_key_path>
"""
import csv
import json
import random
import sys
from collections import defaultdict
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

D = Path(__file__).parent
B = D / "vlm_bench"
KEY = Path(sys.argv[1])
random.seed(7)
meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
det = {r["id"]: r for r in map(json.loads, open(D / "det.jsonl"))}
SEASON = {12: "winter", 1: "winter", 2: "winter", 3: "spring", 4: "spring", 5: "spring",
          6: "summer", 7: "summer", 8: "summer", 9: "autumn", 10: "autumn", 11: "autumn"}

by_horse = defaultdict(list)
for i, r in meta.items():
    if r["date"] and (D / "crops/body" / f"{i}.jpg").exists() and (det[i].get("body_frac") or 0) > 0.03:
        by_horse[r["horse"]].append(i)

queries, gallery = [], {}
for h, ids in sorted(by_horse.items()):
    last = max(meta[i]["date"] for i in ids)
    q = [i for i in ids if meta[i]["date"] == last]
    q = sorted(q, key=lambda i: -(det[i].get("body_frac") or 0))[:2]
    queries += [(i, h) for i in q]
    earlier = [i for i in ids if meta[i]["date"] < last]
    # diversity: round-robin over (view, season) buckets, largest horse first inside a bucket
    buckets = defaultdict(list)
    for i in sorted(earlier, key=lambda i: -(det[i].get("body_frac") or 0)):
        buckets[(det[i]["view"], SEASON[int(meta[i]["date"][5:7])])].append(i)
    pick, keys = [], sorted(buckets, key=lambda k: (k[0] not in ("left", "right"), k))
    while len(pick) < 6 and any(buckets.values()):
        for k in keys:
            if buckets[k] and len(pick) < 6:
                pick.append(buckets[k].pop(0))
    gallery[h] = pick

for sub in ("queries", "gallery"):
    (B / sub).mkdir(parents=True, exist_ok=True)
font = ImageFont.truetype("C:/Windows/Fonts/arial.ttf", 32)
T = 420
for h, pick in gallery.items():
    sheet = Image.new("RGB", (T * 3, T * 2 + 50), "white")
    d = ImageDraw.Draw(sheet)
    d.text((10, 8), f"{h}", fill="black", font=font)
    for k, i in enumerate(pick):
        im = Image.open(D / "crops/body" / f"{i}.jpg").convert("RGB")
        im.thumbnail((T - 10, T - 10))
        sheet.paste(im, ((k % 3) * T + 5, 50 + (k // 3) * T + 5))
    sheet.save(B / "gallery" / f"{h}.jpg", quality=88)

random.shuffle(queries)
key = {}
for n, (i, h) in enumerate(queries, 1):
    im = Image.open(D / "crops/body" / f"{i}.jpg").convert("RGB")
    im.thumbnail((800, 800))
    im.save(B / "queries" / f"q{n:02d}.jpg", quality=90)  # fresh JPEG, no EXIF
    key[f"q{n:02d}"] = {"horse": h, "id": i, "date": meta[i]["date"], "view": det[i]["view"]}
json.dump({"key": key, "gallery": gallery}, open(KEY, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(len(queries), "queries;", {h: len(p) for h, p in gallery.items()})
