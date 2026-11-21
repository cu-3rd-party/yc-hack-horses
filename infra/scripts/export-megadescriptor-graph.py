"""Build the weights-free ONNX graph used with BVRA's pinned checkpoint.

Run this maintainer tool with torch 2.8.0, torchvision 0.23.0, timm 1.0.22 and
onnx 1.19.1. Pass the original pytorch_model.bin and the output .onnx.gz path.
The desktop app needs only ONNX Runtime.
"""

import argparse
import gzip
import hashlib
import io
import math
import re
from pathlib import Path

import onnx
import timm
import torch
from onnx import TensorProto, external_data_helper
from torch._subclasses.fake_tensor import FakeTensorMode


def build(checkpoint, output):
    with FakeTensorMode():
        weights = torch.load(checkpoint, map_location="cpu", weights_only=True)["model"]

    torch.manual_seed(42)
    torch.set_num_threads(2)
    timm.layers.set_fused_attn(False)
    model = timm.create_model("swin_large_patch4_window12_384", pretrained=False, num_classes=0).eval()
    # The exporter must preserve separate parameters even if timm initializes
    # them identically (e.g. two different LayerNorm biases).
    with torch.no_grad():
        for index, param in enumerate(model.parameters(), start=1):
            param.view(-1)[0] = index * 0.0001

    sample = torch.linspace(-1, 1, 3 * 384 * 384).reshape(1, 3, 384, 384)
    buffer = io.BytesIO()
    torch.onnx.export(model, (sample,), buffer, input_names=["pixels"], output_names=["embedding"],
                      export_params=False, do_constant_folding=False, opset_version=17, dynamo=False)
    graph = onnx.load_model_from_string(buffer.getvalue())
    used = set()
    for tensor in list(graph.graph.input)[1:]:
        # timm moved patch merging from the end of one stage to the start of the next.
        name = re.sub(r"layers\.(\d+)\.downsample\.", lambda m: f"layers.{int(m[1])-1}.downsample.", tensor.name)
        weight = weights[name]
        shape = [dim.dim_value for dim in tensor.type.tensor_type.shape.dim]
        if weight.dtype != torch.float32 or list(weight.shape) != shape or not weight.is_contiguous():
            raise ValueError(f"unexpected layout for {name}")
        offset = weight.untyped_storage()._checkpoint_offset + weight.storage_offset() * 4
        external = TensorProto(name=tensor.name, dims=shape, data_type=TensorProto.FLOAT, raw_data=b"")
        external_data_helper.set_external_data(external, location="megadescriptor-l-384.bin",
                                               offset=offset, length=math.prod(shape) * 4)
        external.ClearField("raw_data")
        graph.graph.initializer.append(external)
        used.add(tensor.name)
    if used != set(dict(model.named_parameters())):
        raise ValueError("export did not expose every model parameter")
    del graph.graph.input[1:]
    with output.open("wb") as dst, gzip.GzipFile(filename="", fileobj=dst, mode="wb", mtime=0) as gz:
        gz.write(graph.SerializeToString())


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkpoint", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    with args.checkpoint.open("rb") as source:
        digest = hashlib.file_digest(source, "sha256").hexdigest()
    if digest != "ccfe757f50f7984a115ffe00921cd4c09e260e645215463b23814586040227a3":
        parser.error("expected the BVRA/MegaDescriptor-L-384 checkpoint from revision 33b3c6f")
    build(args.checkpoint, args.output)
