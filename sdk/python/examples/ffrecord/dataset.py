import numpy as np
from typing import List, Union
from filereader import FileReader
# from filereader_dio import FileReader
import torch

class FFRecordDataset:
    def __init__(self, fnames: Union[str, List[str]], check_data: bool = True):
        if isinstance(fnames, str):
            fnames = [fnames]
        self.reader = FileReader(fnames, check_data=check_data)
        self.n = self.reader.n  # 总样本数
        self.reader.close_fd()

        # self.fnames = fnames
        # self.check_data = check_data
        # self.reader = None
        # print("init here", torch.utils.data.get_worker_info())

        # self._init_reader()
        # self.n = self.reader.n
    def _init_reader(self):
        print("init reader+++++++++++++++++++++")
        self.reader.open_fd()

    def __len__(self) -> int:
        return self.n

    def __getitem__(self, index: Union[int, List[int]]) -> Union[np.array, List[np.array]]:
        print("getitem here, ptr of self: ", id(self))
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
    fnames = ["/val2017.ffr"]

    with FFRecordDataset(fnames, check_data=True) as dataset:
        sample = dataset[0]
        print("Sample 0 shape:", sample.shape)

        batch = dataset[[1, 2, 3]]
        print("Batch shape:", [arr.shape for arr in batch])
        print("Dataset length:", len(dataset))