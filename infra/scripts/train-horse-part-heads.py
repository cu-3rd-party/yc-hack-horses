"""Fit or evaluate the four linear heads consumed by reid.decodeParts.

Inputs: VOC2010 JPEG images, matching PASCAL-Part Annotations_Part .mat files,
and the pinned DINOv2-small ONNX file. No additional backbone is downloaded.
"""

import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import subprocess

import numpy as np
from PIL import Image
from scipy.io import loadmat
from scipy.optimize import minimize
from scipy.special import expit


CHANNELS = ("silhouette", "head", "ears", "muzzle")
DINO_SHA = "83141175ec78b4ff9a2bb58a4c7c264ba0054d1c2e122e5a8114b79a8d4179ea"


def held_out(image_id):
    return int.from_bytes(hashlib.sha256(image_id.encode()).digest()[:4], "big") % 5 == 0


def instances(images, annotations, evaluation):
    for path in sorted(images.glob("*.jpg")):
        mat = annotations / (path.stem + ".mat")
        if held_out(path.stem) != evaluation or not mat.exists():
            continue
        anno = loadmat(mat, variable_names=["anno"], squeeze_me=True, struct_as_record=False)["anno"]
        for obj in np.atleast_1d(anno.objects).flat:
            if getattr(obj, "class") != "horse":
                continue
            body = np.asarray(obj.mask, dtype=bool)
            parts = {p.part_name: np.asarray(p.mask, dtype=bool) for p in np.atleast_1d(obj.parts).flat}
            box = Image.fromarray(body).getbbox()
            if not box or min(box[2] - box[0], box[3] - box[1]) < 32:
                continue
            empty = np.zeros_like(body)
            ears = parts.get("lear", empty) | parts.get("rear", empty)
            muzzle = parts.get("muzzle", empty)
            head = parts.get("head", empty) | parts.get("leye", empty) | parts.get("reye", empty) | ears | muzzle
            yield path, box, [body, head & body, ears & body, muzzle & body]


def pad(box, size, fraction):
    x, y, r, b = box
    dx, dy = max(2, int((r - x) * fraction)), max(2, int((b - y) * fraction))
    return max(0, x - dx), max(0, y - dy), min(size[0], r + dx), min(size[1], b + dy)


def features(session, crop):
    pixels = np.asarray(crop.resize((224, 224), Image.Resampling.BICUBIC), dtype=np.float32) / 255
    pixels -= np.array([.485, .456, .406], np.float32)
    pixels /= np.array([.229, .224, .225], np.float32)
    tokens = session.run(["last_hidden_state"], {"pixel_values": pixels.transpose(2, 0, 1)[None]})[0][0, 1:]
    assert tokens.shape == (256, 384)
    if not np.isfinite(tokens).all():
        raise ValueError("DINOv2 returned non-finite features")
    tokens *= 8 / np.maximum(1e-7, np.linalg.norm(tokens, axis=1, keepdims=True))
    y, x = np.indices((16, 16), dtype=np.float32) / 15
    return np.column_stack((tokens, x.ravel(), y.ravel(), np.ones(256, np.float32)))


def occupancy(mask, region, flip):
    cropped = Image.fromarray(mask.astype(np.float32)).crop(region)
    if flip:
        cropped = cropped.transpose(Image.Transpose.FLIP_LEFT_RIGHT)
    return np.asarray(cropped.resize((16, 16), Image.Resampling.BOX)).ravel()


def fit(examples, channel, positive_weight):
    # An absent part may be occluded or unannotated. Use only crops with a label
    # for this channel. Other horses and background in the crop are negatives.
    labeled = [(x, ys[:, channel]) for x, ys in examples if ys[:, channel].any()]
    x = np.concatenate([item[0] for item in labeled])
    y = np.concatenate([item[1] for item in labeled])
    positive = y * positive_weight
    mass = 1 - y + positive
    total = mass.sum(dtype=np.float64)

    def objective(weights):
        logits = x @ weights.astype(np.float32)
        loss = np.sum(mass * np.logaddexp(0, logits) - positive * logits, dtype=np.float64) / total
        grad = (x.T @ (mass * expit(logits) - positive)).astype(np.float64) / total
        penalty = 0.0005
        grad[:-1] += penalty * weights[:-1]
        if not np.isfinite(loss) or not np.isfinite(grad).all():
            raise ValueError("non-finite training objective or gradient")
        return loss + penalty / 2 * (weights[:-1] @ weights[:-1]), grad

    result = minimize(objective, np.zeros(387), jac=True, method="L-BFGS-B",
                      options={"maxiter": 220, "ftol": 1e-9, "gtol": 1e-6})
    if not result.success or not np.isfinite(result.x).all():
        raise ValueError(result.message)
    return result.x


def train(args):
    import onnxruntime as ort
    if hashlib.sha256((args.models / "dinov2-small.onnx").read_bytes()).hexdigest() != DINO_SHA:
        raise ValueError("DINOv2 checkpoint checksum differs")
    options = ort.SessionOptions()
    options.intra_op_num_threads = 4
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(args.models / "dinov2-small.onnx"), options, providers=["CPUExecutionProvider"])
    body_crops, head_crops = [], []
    photos = set()
    count = 0
    for path, box, masks in instances(args.images, args.annotations, False):
        count += 1
        photos.add(path.stem)
        with Image.open(path) as original:
            rgb = original.convert("RGB")
        assert rgb.size == (masks[0].shape[1], masks[0].shape[0])
        head_box = Image.fromarray(masks[1]).getbbox()
        for region, rows, target in [(box, body_crops, masks[:2]), (head_box, head_crops, masks[2:])]:
            if region is None:
                continue
            crop_box = pad(region, rgb.size, .12 if rows is body_crops else .25)
            crop = rgb.crop(crop_box)
            for flip in [False, True]:
                x = features(session, crop.transpose(Image.Transpose.FLIP_LEFT_RIGHT) if flip else crop)
                y = np.column_stack([occupancy(mask, crop_box, flip) for mask in target])
                rows.append((x, y))
        if count % 50 == 0:
            print(f"Extracted {count} horses", flush=True)
    weights = [fit(body_crops, 0, 1.1), fit(body_crops, 1, 1.4),
               fit(head_crops, 0, 1.6), fit(head_crops, 1, 1.35)]
    raw = np.asarray(weights, dtype="<f4").tobytes()
    assert len(raw) == 6192
    (args.models / "part-heads.f32").write_bytes(raw)
    print(f"Trained on {count} horses in {len(photos)} photos. SHA-256 {hashlib.sha256(raw).hexdigest()}")


def evaluate(args):
    # Evaluate through the actual Go two-pass segmenter, including its predicted
    # head crop. Ground-truth boxes are supplied to isolate segmentation from YOLO.
    rows, truth, photos = [], [], set()
    for path, box, masks in instances(args.images, args.annotations, True):
        photos.add(path.stem)
        rows.append(json.dumps({"file": str(path.resolve()), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "box": box}))
        x, y, r, b = box
        w, h = min(128, r-x), min(128, b-y)
        xs = x + ((2*np.arange(w) + 1) * (r-x)) // (2*w)
        ys = y + ((2*np.arange(h) + 1) * (b-y)) // (2*h)
        truth.append(np.stack([m[np.ix_(ys, xs)] for m in masks]))
    cmd = [args.evaluate.resolve(), "-mask-boxes", "-models", args.models, "-runtime", args.runtime]
    result = subprocess.run(cmd, input="\n".join(rows), text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr)
    lines = result.stdout.splitlines()
    assert len(lines) == len(truth) and len(truth) > 0
    intersection, union = np.zeros(4), np.zeros(4)
    for encoded, expected in zip(lines, truth):
        png = json.loads(encoded).get("mask_png")
        bits = np.asarray(Image.open(io.BytesIO(base64.b64decode(png)))) if png else np.zeros(expected.shape[1:], np.uint8)
        assert bits.shape == expected.shape[1:]
        predicted = np.stack([bits & (1 << c) != 0 for c in range(4)])
        intersection += (predicted & expected).sum(axis=(1, 2))
        union += (predicted | expected).sum(axis=(1, 2))
    print(f"Held out {len(truth)} horses in {len(photos)} photos")
    print("Pixel IoU:", dict(zip(CHANNELS, np.round(intersection / union, 4).tolist())))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--images", type=Path, required=True)
    parser.add_argument("--annotations", type=Path, required=True)
    parser.add_argument("--models", type=Path, required=True)
    parser.add_argument("--evaluate", type=Path, metavar="HORSE_EMBED")
    parser.add_argument("--runtime", type=Path)
    args = parser.parse_args()
    if args.evaluate and not args.runtime:
        parser.error("--evaluate requires --runtime")
    (evaluate if args.evaluate else train)(args)
