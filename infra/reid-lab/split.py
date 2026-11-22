"""Fixed, honest train/val/test split for the last-2-years data.

- Only photos shot on/after CUTOFF (default: 2 years before 2026-11-22) with a detected horse.
- Group = "trip": a horse's shooting days merged when <= TRIP_DAYS apart (consecutive days never straddle splits).
- TEST: per horse one trip (if the horse has >= 2 trips), chosen greedily so the season mix of the test set
  follows the overall season mix (season stratification); ties -> the most recent trip (realistic "new visit").
- VAL: from the remaining trips, one more trip per horse if it still keeps >= 1 trip for train. Used ONLY for tuning.
- Horses with a single trip stay in train (cannot be tested honestly).
Writes split.json {id: "train"|"val"|"test"} and prints the table.
"""
import csv
import json
import os
from collections import Counter, defaultdict
from datetime import date
from pathlib import Path

D = Path(__file__).parent
CUTOFF = os.environ.get("CUTOFF", "2024-11-22")
TRIP_DAYS = int(os.environ.get("TRIP_DAYS", 7))
SEASON = {12: "winter", 1: "winter", 2: "winter", 3: "spring", 4: "spring", 5: "spring",
          6: "summer", 7: "summer", 8: "summer", 9: "autumn", 10: "autumn", 11: "autumn"}

meta = {r["id"]: r for r in csv.DictReader(open(D / "meta.csv", encoding="utf-8"))}
det = {r["id"]: r for r in map(json.loads, open(D / "det.jsonl"))}
ids = [i for i, r in meta.items() if r["date"] and r["date"] >= CUTOFF and det[i]["body"]]

# trips
trips = defaultdict(list)  # horse -> list of trips, each a list of photo ids
for h in sorted({meta[i]["horse"] for i in ids}):
    hid = sorted((i for i in ids if meta[i]["horse"] == h), key=lambda i: meta[i]["date"])
    cur, prev = [], None
    for i in hid:
        d = date.fromisoformat(meta[i]["date"]).toordinal()
        if prev is not None and d - prev > TRIP_DAYS:
            trips[h].append(cur)
            cur = []
        cur.append(i)
        prev = d
    trips[h].append(cur)


def season_of(trip):
    return Counter(SEASON[int(meta[i]["date"][5:7])] for i in trip).most_common(1)[0][0]


overall = Counter(SEASON[int(meta[i]["date"][5:7])] for i in ids)
total = sum(overall.values())
split = {i: "train" for i in ids}
test_season = Counter()


def pick(h, pool, counter):
    """choose the trip whose season is most under-represented in `counter`; ties -> latest."""
    def deficit(t):
        s = season_of(t)
        have = counter[s] / max(1, sum(counter.values()))
        return overall[s] / total - have
    return max(pool, key=lambda t: (round(deficit(t), 3), meta[t[-1]]["date"]))


# horses with fewer trips first: they have the fewest choices
for h in sorted(trips, key=lambda h: (len(trips[h]), h)):
    if len(trips[h]) < 2:
        continue
    t = pick(h, trips[h], test_season)
    for i in t:
        split[i] = "test"
    test_season[season_of(t)] += len(t)
val_season = Counter()
for h in sorted(trips, key=lambda h: (len(trips[h]), h)):
    rest = [t for t in trips[h] if split[t[0]] == "train"]
    if len(rest) < 2:
        continue
    t = pick(h, rest, val_season)
    for i in t:
        split[i] = "val"
    val_season[season_of(t)] += len(t)

json.dump(split, open(D / "split.json", "w", encoding="utf-8"), indent=0)
json.dump({i: f"{h}|{k}" for h in trips for k, t in enumerate(trips[h]) for i in t},
          open(D / "trips.json", "w", encoding="utf-8"), ensure_ascii=False, indent=0)
c = Counter(split.values())
print(f"cutoff {CUTOFF}, trip gap {TRIP_DAYS}d: {len(ids)} photos -> {dict(c)}")
print("season mix  overall:", {s: round(overall[s] / total, 2) for s in overall})
for part in ("train", "val", "test"):
    sc = Counter(SEASON[int(meta[i]["date"][5:7])] for i in ids if split[i] == part)
    print(f"  {part:5}:", dict(sc))
print("\nhorse     trips  train  val  test   test season(s)")
for h in sorted(trips):
    n = Counter(split[i] for t in trips[h] for i in t)
    ts = sorted({SEASON[int(meta[i]["date"][5:7])] for t in trips[h] for i in t if split[i] == "test"})
    tr = sorted({SEASON[int(meta[i]["date"][5:7])] for t in trips[h] for i in t if split[i] != "test"})
    print(f"{h:9} {len(trips[h]):5} {n['train']:6} {n['val']:4} {n['test']:5}   test={ts} train/val={tr}")
