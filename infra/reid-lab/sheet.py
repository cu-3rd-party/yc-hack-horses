"""Contact sheet: photo with body (green) and head (red) boxes plus the view label."""
import json
import sys
from pathlib import Path

from PIL import Image, ImageDraw

D = Path(__file__).parent
recs = [json.loads(l) for l in open(D / "det.jsonl")]
recs = recs[int(sys.argv[1]) if len(sys.argv) > 1 else 0:][::int(sys.argv[2]) if len(sys.argv) > 2 else 1][:24]
T = 320
sheet = Image.new("RGB", (T * 6, T * 4), "black")
for k, r in enumerate(recs):
    img = Image.open(D / "img" / f"{int(r['id']):05d}.jpg")
    s = T / max(img.size)
    img = img.resize((int(img.width * s), int(img.height * s)))
    d = ImageDraw.Draw(img)
    if r["body"]:
        d.rectangle([v * s for v in r["body"]], outline="lime", width=3)
    if r["head"]:
        d.rectangle([v * s for v in r["head"]], outline="red", width=3)
    d.rectangle([0, 0, 120, 16], fill="black")
    d.text((3, 2), f"{r['id']} {r['view']} n={r['n_horses']}", fill="yellow")
    sheet.paste(img, ((k % 6) * T, (k // 6) * T))
sheet.save(D / "sheet.jpg", quality=85)
