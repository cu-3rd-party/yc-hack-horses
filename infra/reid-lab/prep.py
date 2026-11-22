"""Index the organisers' photo bank: downscaled JPEG copies + shooting date per photo.

Writes img/<id>.jpg and meta.csv (id, horse, date, src).
"""
import csv
import os
import re
import sys
from concurrent.futures import ProcessPoolExecutor
from datetime import datetime
from pathlib import Path

from PIL import Image, ImageOps

BANK = Path(os.environ.get("HORSE_BANK", Path.home() / "yc-hack-horses/data/extracted/Фотобанк_Хакатон"))
OUT = Path(__file__).parent
MAX_SIDE = 1280


def shot_date(img, name):
    exif = img.getexif()
    raw = exif.get_ifd(0x8769).get(36867) or exif.get(306)
    if raw:
        try:
            return datetime.strptime(str(raw).strip()[:19], "%Y:%m:%d %H:%M:%S").date().isoformat()
        except ValueError:
            pass
    m = re.search(r"(20\d{2})(\d{2})(\d{2})", name)
    return f"{m[1]}-{m[2]}-{m[3]}" if m else ""


def work(job):
    i, src = job
    img = Image.open(src)
    date = shot_date(img, src.name)
    img.draft("RGB", (MAX_SIDE, MAX_SIDE))
    img = ImageOps.exif_transpose(img).convert("RGB")
    img.thumbnail((MAX_SIDE, MAX_SIDE))
    img.save(OUT / "img" / f"{i:05d}.jpg", quality=92)
    return i, date


def main():
    (OUT / "img").mkdir(exist_ok=True)
    srcs = sorted(p for p in BANK.glob("*/*") if p.suffix.lower() in {".jpg", ".jpeg"})
    jobs = list(enumerate(srcs))
    with ProcessPoolExecutor(int(sys.argv[1]) if len(sys.argv) > 1 else 6) as ex:
        dates = dict(ex.map(work, jobs, chunksize=8))
    with open(OUT / "meta.csv", "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["id", "horse", "date", "src"])
        for i, src in jobs:
            w.writerow([i, src.parent.name.split(" (")[0], dates[i], src.relative_to(BANK).as_posix()])
    print(len(jobs), "photos;", sum(1 for d in dates.values() if not d), "without date")


if __name__ == "__main__":
    main()
