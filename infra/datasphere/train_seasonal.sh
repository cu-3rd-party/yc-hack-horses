#!/usr/bin/env bash
set -eu -o pipefail
mkdir -p seasonal
tar -xf "$1"
if python3 -c 'import torch,numpy; assert torch.cuda.is_available()'; then
    py=python3
else
    python3 -m venv env
    py=env/bin/python
    "$py" -m pip -q install torch==2.5.1 --index-url https://download.pytorch.org/whl/cu118
    "$py" -m pip -q install numpy==1.26.4
fi
OPENBLAS_NUM_THREADS=2 OMP_NUM_THREADS=2 "$py" -u train_seasonal.py 2>&1 | tee seasonal/train.log
tar -czf "$2" seasonal
