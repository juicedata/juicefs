import numpy as np
from typing import List, Iterator, Callable
from multiprocessing import Pool
from dataset import FFRecordDataset
import os


class FFRecordDataLoader:
    def __init__(
            self,
            dataset: FFRecordDataset,
            batch_size: int = 32,
            shuffle: bool = False,
            num_workers: int = 0,
            worker_init_fn: Callable = None,
    ):
        self.dataset = dataset
        self.batch_size = batch_size
        self.shuffle = shuffle
        self.num_workers = num_workers
        self.indices = np.arange(len(dataset))
        self.worker_init_fn = worker_init_fn


        if self.shuffle:
            np.random.shuffle(self.indices)

    def __iter__(self) -> Iterator[List[np.array]]:
        batches = [
            self.indices[i : i + self.batch_size]
            for i in range(0, len(self.indices), self.batch_size)
        ]

        if self.num_workers > 0:
            with Pool(
                    processes=self.num_workers, initializer=self.worker_init_fn
            ) as pool:
                for batch_indices in batches:
                    batch_indices = [int(i) for i in batch_indices]
                    yield pool.map(self.dataset.__getitem__, batch_indices)
        else:
            for batch_indices in batches:
                batch_indices = [int(i) for i in batch_indices]
                yield [self.dataset[i] for i in batch_indices]

    def __len__(self) -> int:
        return (len(self.dataset) + self.batch_size - 1) // self.batch_size


if __name__ == "__main__":
    fnames = ["/val2017.ffr"]

    dataset = FFRecordDataset(fnames, check_data=True)

    def worker_init_fn():
        print("Worker initialized")
        dataset._init_reader()

    print("pid: ", os.getpid())

    # dataloader = FFRecordDataLoader(dataset, batch_size=2, shuffle=True);worker_init_fn()
    dataloader = FFRecordDataLoader(dataset, batch_size=2, shuffle=True, worker_init_fn=worker_init_fn)
    # dataloader = FFRecordDataLoader(dataset, batch_size=1, shuffle=True, num_workers=1, worker_init_fn=worker_init_fn)
    # dataloader = FFRecordDataLoader(dataset, batch_size=32, shuffle=True, num_workers=1, worker_init_fn=worker_init_fn)

    i=0
    for batch in dataloader:
        print("Batch shape:", [arr.shape for arr in batch])
        i+=1
        if i>1:
            break