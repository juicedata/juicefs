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
from typing import List, Iterator, Callable
from multiprocessing import Pool
from dataset import FFRecordDataset
import os
import torch
import time

class FFRecordDataLoader(torch.utils.data.DataLoader):
    def __init__(
                self,
                dataset: FFRecordDataset,
                batch_size=1,
                shuffle: bool = False,
                sampler=None,
                batch_sampler=None,
                num_workers: int = 0,
                collate_fn=None,
                pin_memory: bool = False,
                drop_last: bool = False,
                timeout: float = 0,
                worker_init_fn=None,
                generator=None,
                *,
                prefetch_factor: int = 2,
                persistent_workers: bool = False,
                skippable: bool = True):

        # use fork to create subprocesses
        if num_workers == 0:
            multiprocessing_context = None
            dataset.initialize()
        else:
            multiprocessing_context = 'fork'
        self.skippable = skippable

        super(FFRecordDataLoader,
              self).__init__(dataset=dataset,
                             batch_size=batch_size,
                             shuffle=shuffle,
                             sampler=sampler,
                             batch_sampler=batch_sampler,
                             num_workers=num_workers,
                             collate_fn=collate_fn,
                             pin_memory=pin_memory,
                             drop_last=drop_last,
                             timeout=timeout,
                             worker_init_fn=worker_init_fn,
                             multiprocessing_context=multiprocessing_context,
                             generator=generator,
                             prefetch_factor=prefetch_factor,
                             persistent_workers=persistent_workers)

if __name__ == "__main__":
    fnames = ["/demo.ffr"]

    dataset = FFRecordDataset(fnames, check_data=True)

    def worker_init_fn(worker_id):
        worker_info = torch.utils.data.get_worker_info()
        print(f"Worker initialized pid: {os.getpid()}, work_info: {worker_info}")
        dataset = worker_info.dataset
        dataset.initialize(worker_id=worker_id)

    def collate_fn(batch):
        return batch

    begin_time = time.time()

    dataloader = FFRecordDataLoader(dataset, batch_size=1, shuffle=True, num_workers=10, worker_init_fn=worker_init_fn, prefetch_factor=None, collate_fn=collate_fn)

    i=0
    for batch in dataloader:
        # print(i, ": ", batch[0]["index"], "----", time.time()-begin_time)
        i+=1
        if i>1000:
            break
    end_time = time.time()
    print(f"takes: {end_time-begin_time}")
