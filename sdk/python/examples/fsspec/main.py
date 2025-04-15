import fsspec
import ray
import sys
sys.path.append('.')
import sdk.python.juicefs.juicefs.spec
# from sdk.python.juicefs.juicefs.spec import JuiceFS

fs = fsspec.filesystem('https')
ds = ray.data.read_csv(
    "https://gender-pay-gap.service.gov.uk/viewing/download-data/2021",
    filesystem=fs,
    partition_filter=None # Since the file doesn't end in .csv
)
ds.count()

print("----++++----++++----")

jfs = fsspec.filesystem("jfs", auto_mkdir=True, name="myjfs", meta="redis://localhost")
dsjfs = ray.data.read_csv('/ray_demo_data.csv', filesystem=jfs)
dsjfs.count()

