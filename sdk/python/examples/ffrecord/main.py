# encoding: utf-8
# JuiceFS, Copyright 2025 Juicedata, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import sys
from pathlib import Path
import loguru
import random
import pickle
from multiprocessing import Pool
import numpy as np
from PIL import Image
from faker import Faker
import io
import time
from tqdm import tqdm
from ffrecord import FileWriter
from ffrecord.torch import Dataset, DataLoader
from ffrecord import FileReader

logger = loguru.logger
fake = Faker()

def serialize(sample):
    return pickle.dumps(sample)

def deserialize(sample):
    return pickle.loads(sample)

def generate_random_image_np(
    width=256,
    height=256,
    format="JPEG",  # JPEG, PNG, WEBP
    quality=90,     # only for JPEG/WEBP
):
    image_np = np.random.randint(0, 255, (height, width, 3), dtype=np.uint8)
    img = Image.fromarray(image_np)
    
    img_bytes = io.BytesIO()
    img.save(img_bytes, format=format, quality=quality)
    return img_bytes.getvalue()

def generate_data_entry(
    idx,
    text=None,
    avg_width=1024,
    avg_height=1024,
    variance=50,
    possible_formats=["PNG"],
    # possible_formats=["JPEG", "PNG", "WEBP"],
):
    """
    - avg_width/avg_height ± variance
    """
    image_format = random.choice(possible_formats).lower()

    width = random.randint(avg_width - variance, avg_width + variance)
    height = random.randint(avg_height - variance, avg_height + variance)
    width, height = max(width, 32), max(height, 32) 
    
    img_bytes = generate_random_image_np(
        width=width,
        height=height,
        format=image_format.upper(),
    )
    
    if text is None:
        text = fake.sentence()
    
    return {
        "index": idx,
        "txt": text,
        image_format: img_bytes,
    }

def write_ffrecord():
  ffr_output = Path(ffrecord_file)
  if ffr_output.exists():
    logger.warning(f"Output {ffr_output} exists, removing")    
  logger.info(f"Generating {num_samples} samples")
  with Pool(num_proc) as pool:
      data_to_write = list(
          tqdm(
              pool.imap_unordered(generate_data_entry, range(num_samples), chunksize=10),
              total=num_samples,
              desc="Generating data"
              )
            )
  begin_time = time.time()
  writer = FileWriter(ffr_output, len(data_to_write))
  for i, data in enumerate(data_to_write):
      writer.write_one(serialize(data))
      # writer.write_one(data)
  writer.close()
  end_time = time.time()
  lmdb_size = ffr_output.stat().st_size
  logger.info(f"FFRecord size: {lmdb_size / 1024 ** 3:.2f} GB")
  logger.info(f"Time taken to write: {end_time - begin_time:.2f} seconds")

def read_ffrecord(batch_size: int):
    reader = FileReader([ffrecord_file], check_data=True)

    sample_indices = list(range(num_samples))
    random.Random(0).shuffle(sample_indices)
    sample_batches = [sample_indices[i: i + batch_size] for i in range(0, len(sample_indices), batch_size)]
    logger.info(f'Number of samples to read: {reader.n}, batch_size = {batch_size}, num_batches = {len(sample_batches)}')
    read_indices = set()
    begin_time = time.time()
    index_iter = sample_batches
    index_iter = tqdm(index_iter, desc="Reading data in batches", total=len(sample_batches))

    for indices in index_iter:
        all_data = reader.read(indices)
        for data in all_data:
            data = deserialize(data)
            read_indices.add(data["index"])
    end_time = time.time()
    reader.close()
    assert read_indices == set(range(num_samples))
    logger.info(f"Read {len(read_indices)} samples in {end_time - begin_time:.2f} s: {len(read_indices) / (end_time - begin_time):.2f} samples/s")


class MyDataset(Dataset):
    def __init__(self, fnames, check_data=True):
        self.reader = FileReader(fnames, check_data=check_data)

    def __len__(self):
        return self.reader.n

    def __getitem__(self, indices):
        data = self.reader.read(indices)
        samples = []

        for bytes_ in data:
            item = pickle.loads(bytes_)
            samples.append(item)

        return samples


ffrecord_file="/tmp/jfs/demo.ffr"
num_samples=1000
num_proc=4

if __name__ == "__main__":
    if len(sys.argv) > 1:
        if sys.argv[1] == "write":
            write_ffrecord()
        elif sys.argv[1] == "read":
            read_ffrecord(batch_size=1)
    else:
        begin_time = time.time()
        dataset = MyDataset([ffrecord_file], check_data=True)
        dataloader = DataLoader(dataset, batch_size=1, shuffle=True, num_workers=10,prefetch_factor=None)

        i=0
        for batch in dataloader:
            i+=1
            if i>1000:
                break
        end_time = time.time()
        print(f"takes: {end_time-begin_time}")
