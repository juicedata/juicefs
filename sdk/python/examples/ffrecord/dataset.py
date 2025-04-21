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

import numpy as np
from typing import List, Union
from filereader import FileReader
# from filereader_dio import FileReader
import torch
import os

class FFRecordDataset(torch.utils.data.Dataset):
    def __init__(self, fnames: Union[str, List[str]], check_data: bool = True):
        if isinstance(fnames, str):
            fnames = [fnames]
        self.reader = FileReader(fnames, check_data=check_data)
        self.n = self.reader.n
        self.reader.close_fd()

    def initialize(self, worker_id=0, num_workers=1):
        self.reader.open_fd()
        self.n = self.reader.n

    def __len__(self) -> int:
        return self.n

    def __getitem__(self, index: Union[int, List[int]]) -> Union[np.array, List[np.array]]:
        if isinstance(index, int):
            return self.reader.read_one(index)
        elif isinstance(index, list):
            return self.reader.read_batch(index)
        else:
            raise TypeError(f"Index must be int or list, got {type(index)}")

    def close(self):
        self.reader.close_fd()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()


if __name__ == "__main__":
    fnames = ["/demo.ffr"]

    with FFRecordDataset(fnames, check_data=True) as dataset:
        sample = dataset[0]
        print("Sample 0:", sample)

        batch = dataset[[1, 2, 3]]
        print(batch)
        print("Dataset length:", len(dataset))
