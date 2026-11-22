"""Export the final three-epoch DataSphere bbox adapter as row-major float32 LE."""

import argparse
import hashlib
from pathlib import Path

import numpy as np
import torch


# These pin the final output of bt1fe8ib3o3j6o4os5ij and the base descriptor.
CHECKPOINT_SHA256 = "1374c453653083aab6786c58f4d675a137b063b2477b539128779ead73a24fd5"
REVISION = "33b3c6f4ee0c386a4126cc3dcd23843920613fa1"


def export(checkpoint, destination):
    if hashlib.sha256(checkpoint.read_bytes()).hexdigest() != CHECKPOINT_SHA256:
        raise ValueError("Unexpected seasonal training checkpoint")
    saved = torch.load(checkpoint, map_location="cpu", weights_only=True)
    if saved["epoch"] != 3 or saved["revision"] != REVISION:
        raise ValueError("Unexpected training epoch or base model revision")
    state = saved["state_dict"]
    matrices = []
    for name, shape in (("down.weight", (64, 1536)), ("up.weight", (1536, 64))):
        tensor = state[name]
        if tuple(tensor.shape) != shape or tensor.dtype != torch.float32:
            raise ValueError(f"Incompatible tensor {name}")
        values = tensor.numpy()
        if not np.isfinite(values).all():
            raise ValueError(f"Non-finite tensor {name}")
        matrices.append(values.astype("<f4", copy=False).tobytes(order="C"))
    destination.write_bytes(b"".join(matrices))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkpoint", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    export(args.checkpoint, args.destination)
