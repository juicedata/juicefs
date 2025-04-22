```bash
# This is a ffrecord dataloader example.
# Prepare
# Install ffrecord here: https://github.com/HFAiLab/ffrecord
# Mount JuiceFS
juicefs mount redis://localhost /tmp/jfs -d

# Generate dataset
python3 sdk/python/examples/ffrecord/main.py write
# Simple read dataset
python3 sdk/python/examples/ffrecord/main.py read
# Read dataset with dataloader: (takes 39.55s)
python3 sdk/python/examples/ffrecord/main.py

# Prepare python-sdk
make -C sdk/python libjfs.so
# Read dataset with Juicefs-pythonsdk-dataloader: (takes 10.02s)
python3 sdk/python/examples/ffrecord/dataloader.py
```