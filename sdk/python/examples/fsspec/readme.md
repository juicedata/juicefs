```bash
# This example demonstrates how to use the fsspec library to read a CSV file.
juicefs mount redis://localhost /tmp/jfs -d
# Download the data file
wget https://static.juicefs.com/misc/ray_demo_data.csv -O /tmp/jfs/ray_demo_data.csv

# run the example
python3 sdk/python/examples/fsspec/main.py
```