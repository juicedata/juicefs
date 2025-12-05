#!/bin/bash
# ai_format_benchmark.sh
set -e

MNT_POINT=$1
RESULTS_FILE=$2
VERSION=$3

# Create Python virtual environment if needed
if [ ! -d "venv" ]; then
    PY_VER=$(python3 -V 2>&1 | awk '{print $2}' | cut -d. -f1,2)
    PKG="python${PY_VER}-venv"
    sudo apt install $PKG -y
    python3 -m venv venv
fi

source venv/bin/activate

# Install required packages
#pip install --upgrade pip
pip install numpy pandas

# Try to install optional dependencies
pip install h5py || echo "h5py installation failed, HDF5 tests will be skipped"
pip install torch || echo "PyTorch installation failed, PyTorch tests will be skipped"
pip install tensorflow || echo "TensorFlow installation failed, TensorFlow tests will be skipped"
pip install pyarrow || echo "PyArrow installation failed, Parquet tests will be skipped"
pip install onnx || echo "OONX installation failed, ONNX tests will be skipped"
pip install onnxruntime
pip install pillow
pip install lmdb
pip install tqdm


# Run the benchmark
python .github/scripts/perf/ai_format_benchmark.py "$MNT_POINT" "$RESULTS_FILE" "$VERSION"

deactivate
