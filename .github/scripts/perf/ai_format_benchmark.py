#!/usr/bin/env python3
"""
AI Training Format Performance Benchmark Script - Fixed Version
Comprehensive performance testing for AI training file formats
"""

import os
import sys
import json
import time
import tempfile
import subprocess
import numpy as np
import pandas as pd
from pathlib import Path
from typing import Dict, List, Tuple, Any, Callable, Optional
import argparse
import shutil
from dataclasses import dataclass
import pickle
import random
import io
from PIL import Image
import lmdb
from concurrent.futures import ProcessPoolExecutor, as_completed
from tqdm import tqdm

try:
    import h5py
except ImportError:
    h5py = None

try:
    import torch
except ImportError:
    torch = None

try:
    import tensorflow as tf
except ImportError:
    tf = None

try:
    import pyarrow.parquet as pq
    import pyarrow as pa
except ImportError:
    pq = None
    pa = None

try:
    import onnx
    import onnxruntime as ort
except ImportError:
    onnx = None
    ort = None

@dataclass
class BenchmarkResult:
    """Structured benchmark result"""
    min_time: float
    max_time: float
    mean_time: float
    std_time: float
    throughput_mb_s: Optional[float] = None
    file_size_bytes: Optional[int] = None
    operation_count: Optional[int] = None
    details: Dict[str, Any] = None

class AIFormatBenchmark:
    def __init__(self, mount_point: str, results_file: str, version: str):
        self.mount_point = Path(mount_point)
        self.results_file = Path(results_file)
        self.version = version
        self.results = {}
        self.verbose = False

        # Test configuration
        self.config = {
            'small_file_mb': 50,
            'medium_file_mb': 100,
            'large_file_mb': 200,
            'num_runs': 2,
            'cool_down_time': 0.5,
            'num_samples': 5000,  # For dataset benchmarks
            'image_size': (128, 128, 3),  # For image dataset benchmarks
            'lmdb_num_samples': 1000,  # Reduced for CI testing
            'lmdb_num_proc': 4,  # Reduced for CI testing
            'lmdb_image_size': (128, 128)  # Smaller images for CI
        }

        # Create test directory
        self.test_dir = self.mount_point / "ai_benchmark"
        self.test_dir.mkdir(exist_ok=True)

    def clear_cache(self):
        """Clear system cache silently"""
        try:
            subprocess.run(["sudo", "sync"], check=True,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(
                "echo 3 | sudo tee /proc/sys/vm/drop_caches > /dev/null",
                shell=True,
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL
            )
        except (subprocess.CalledProcessError, FileNotFoundError):
            if self.verbose:
                print("Warning: Failed to clear cache (requires sudo privileges)")

    def run_benchmark(self, name: str, func: Callable, file_size: int = None,
                     description: str = "") -> Optional[BenchmarkResult]:
        """Run a benchmark function multiple times and calculate statistics"""
        times = []
        size_results = []

        if self.verbose:
            print(f"  Running {name}: {description}")

        for i in range(self.config['num_runs']):
            self.clear_cache()
            start_time = time.perf_counter()
            try:
                result = func()
            except Exception as e:
                print(f"    Error in run {i+1}: {e}")
                result = None
            end_time = time.perf_counter()

            elapsed_time = end_time - start_time
            times.append(elapsed_time)
            
            if "file_size" in result:
                file_size = result["file_size"]
            if result is not None:
                size_results.append(result)
            if tf is not None:
                tf.keras.backend.clear_session()
            if torch is not None:
                torch.cuda.empty_cache() if torch.cuda.is_available() else None
            import gc
            gc.collect()
            time.sleep(self.config['cool_down_time'])

        if not times:
            return None

        stats = BenchmarkResult(
            min_time=min(times),
            max_time=max(times),
            mean_time=np.mean(times),
            std_time=np.std(times),
            details=size_results[0] if size_results else {}
        )

        if file_size is not None and stats.mean_time > 0:
            if "num_layers" in result:
                stats.throughput_mb_s = file_size / result["num_layers"] / stats.mean_time / (1024**2)
            else:
                stats.throughput_mb_s = file_size / stats.mean_time / (1024**2)
            stats.file_size_bytes = file_size

        if stats.details:
            for key in ['num_records', 'records_read', 'num_files', 'num_layers', 'num_samples']:
                if key in stats.details:
                    stats.operation_count = stats.details[key]
                    break

        if self.verbose:
            self._print_benchmark_result(name, stats)

        return stats

    def _print_benchmark_result(self, name: str, stats: BenchmarkResult):
        """Print individual benchmark result in structured format"""
        print(f"    {name}:")
        print(f"      Time: {stats.mean_time:.3f}s ± {stats.std_time:.3f}s "
              f"(min: {stats.min_time:.3f}s, max: {stats.max_time:.3f}s)")

        if stats.throughput_mb_s is not None:
            print(f"      Throughput: {stats.throughput_mb_s:.2f} MB/s")

        if stats.file_size_bytes is not None:
            size_mb = stats.file_size_bytes / (1024**2)
            print(f"      File size: {size_mb:.1f} MB")

        if stats.operation_count is not None:
            print(f"      Operations: {stats.operation_count:,}")

        if stats.details:
            details_str = ", ".join([f"{k}: {v}" for k, v in stats.details.items()])
            print(f"      Details: {details_str}")
    
    def generate_random_image_bytes(self, width=64, height=64, format="JPEG", quality=85):
        """Generate random image bytes for LMDB testing"""
        image_np = np.random.randint(0, 255, (height, width, 3), dtype=np.uint8)
        img = Image.fromarray(image_np)
        img_bytes = io.BytesIO()
        img.save(img_bytes, format=format, quality=quality)
        return img_bytes.getvalue()

    def generate_lmdb_data_entry(self, idx, image_size=(64, 64)):
        """Generate a single LMDB data entry"""
        img_bytes = self.generate_random_image_bytes(width=image_size[0], height=image_size[1])
        return {
            "index": idx,
            "txt": f"Sample text for entry {idx}",
            "jpeg": img_bytes
        }

    def write_lmdb_data(self, lmdb_path, num_samples, image_size=(64, 64)):
        """Write data to LMDB database"""
        env = lmdb.open(str(lmdb_path), readonly=False, meminit=False, map_size=1024**4)
        total_bytes = 0
        
        with env.begin(write=True) as txn:
            for i in range(num_samples):
                data = self.generate_lmdb_data_entry(i, image_size)
                key = str(i).encode()
                value = pickle.dumps(data)
                txn.put(key, value)
                total_bytes += len(value)
        
        env.close()
        return total_bytes

    def read_lmdb_data_single_process(self, lmdb_path):
        """Read LMDB data using single process"""
        env = lmdb.open(str(lmdb_path), readonly=True, lock=False, readahead=False, meminit=False)
        total_bytes = 0
        samples_read = 0
        
        with env.begin(write=False) as txn:
            cursor = txn.cursor()
            for key, value in cursor:
                total_bytes += len(value)
                samples_read += 1
        
        env.close()
        return total_bytes, samples_read

    def lmdb_batch_worker(self, lmdb_path, key_batch):
        """Worker function for multi-process LMDB reading"""
        env = lmdb.open(str(lmdb_path), readonly=True, lock=False, readahead=False, meminit=False)
        total_bytes = 0
        samples_processed = 0
        
        with env.begin(write=False) as txn:
            for key_bytes in key_batch:
                data = txn.get(key_bytes)
                if data:
                    total_bytes += len(data)
                    samples_processed += 1
        
        env.close()
        return samples_processed, total_bytes

    def read_lmdb_data_multi_process(self, lmdb_path, num_processes=2):
        """Read LMDB data using multiple processes"""
        env = lmdb.open(str(lmdb_path), readonly=True, lock=False, readahead=False, meminit=False)
        
        # Get all keys
        keys = []
        with env.begin(write=False) as txn:
            cursor = txn.cursor()
            for key, _ in cursor:
                keys.append(key)
        
        env.close()
        
        # Split keys into batches for each process
        batch_size = len(keys) // num_processes + 1
        key_batches = [keys[i:i + batch_size] for i in range(0, len(keys), batch_size)]
        
        total_bytes = 0
        total_samples = 0
        with ProcessPoolExecutor(max_workers=num_processes) as executor:
            futures = []
            for batch in key_batches:
                futures.append(executor.submit(self.lmdb_batch_worker, lmdb_path, batch))
            
            for future in as_completed(futures):
                samples, bytes_read = future.result()
                total_samples += samples
                total_bytes += bytes_read
        
        return total_bytes, total_samples
    
    def benchmark_lmdb(self):
        """Benchmark LMDB format for datasets"""
        results = {}
        num_samples = self.config['lmdb_num_samples']
        num_proc = self.config['lmdb_num_proc']
        image_size = self.config['lmdb_image_size']
        
        # Estimate file size (approx 5KB per sample)
        estimated_file_size = num_samples * 5 * 1024
        
        for size_name, sample_multiplier in [('small', 1), ('medium', 2)]:
            actual_samples = num_samples * sample_multiplier
            lmdb_dir = self.test_dir / f"lmdb_{size_name}_{actual_samples}samples"
            lmdb_dir.mkdir(exist_ok=True)
            
            def write_func():
                total_bytes = self.write_lmdb_data(lmdb_dir, actual_samples, image_size)
                return {"file_size": total_bytes, "num_samples": actual_samples}
            
            def read_single_func():
                bytes_read, samples_read = self.read_lmdb_data_single_process(lmdb_dir)
                return {"bytes_read": bytes_read, "samples_read": samples_read}

            def read_multi_func():
                bytes_read, samples_read = self.read_lmdb_data_multi_process(lmdb_dir, num_proc)
                return {"bytes_read": bytes_read, "samples_read": samples_read, "processes": num_proc}
            
            # Write benchmark
            write_stats = self.run_benchmark(
                f"lmdb_{size_name}_write", write_func, file_size=estimated_file_size * sample_multiplier,
                description=f"Write LMDB ({actual_samples} samples)"
            )
            
            # Single process read benchmark
            read_single_stats = self.run_benchmark(
                f"lmdb_{size_name}_read_single", read_single_func, file_size=estimated_file_size * sample_multiplier,
                description=f"Read LMDB single process ({actual_samples} samples)"
            )
            read_multi_stats = self.run_benchmark(
                f"lmdb_{size_name}_read_multi", read_multi_func, file_size=estimated_file_size * sample_multiplier,
                description=f"Read LMDB multi process ({num_proc} processes, {actual_samples} samples)"
            )
            
            # Cleanup
            if lmdb_dir.exists():
                shutil.rmtree(lmdb_dir)
            
            if write_stats and read_single_stats and read_multi_stats:
                results[size_name] = {
                    "write": write_stats,
                    "read_single": read_single_stats,
                    "read_multi": read_multi_stats
                }
        
        return results


    # ----------------------------------------------------------------------
    # Model Weights Benchmarks
    # ----------------------------------------------------------------------

    def benchmark_pytorch_weights(self):
        """Benchmark PyTorch .pt/.pth format with multiple sizes"""
        if torch is None:
            print("PyTorch not available, skipping PyTorch benchmark")
            return None

        results = {}
        for size_name, size_mb in [('small', 1000), ('large', 4000)]:
            file_path = self.test_dir / f"pytorch_weights_{size_name}_{size_mb}mb.pt"
            file_size = size_mb * 1024 * 1024

            layer_sizes = [file_size // 8 // 5] * 5  # Split into 5 layers
            dummy_data = {
                'weights': {f'layer_{i}': torch.randn(size) for i, size in enumerate(layer_sizes)},
                'optimizer': {'lr': 0.001, 'momentum': 0.9},
                'metadata': {'epoch': 10, 'version': '1.0', 'created': time.time()}
            }

            def write_func():
                torch.save(dummy_data, file_path)
                actual_size = os.path.getsize(file_path)
                return {'file_size': actual_size, 'num_layers': len(dummy_data['weights'])}

            def read_func():
                loaded = torch.load(file_path)
                total_params = 0
                for layer_name, weights in loaded['weights'].items():
                    total_params += weights.numel()
                    _ = torch.sum(weights).item() % 1000
                return {'file_size': total_params, 'num_layers': len(loaded['weights'])}

            write_stats = self.run_benchmark(
                f"pytorch_weights_{size_name}_write", write_func, file_size=file_size / 2,
                description=f"Write PyTorch weights ({size_mb}MB)"
            )

            read_stats = self.run_benchmark(
                f"pytorch_weights_{size_name}_read", read_func, file_size=file_size / 2,
                description=f"Read PyTorch weights ({size_mb}MB)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_tensorflow_h5(self):
        """Benchmark TensorFlow HDF5 format with multiple sizes"""
        if tf is None or h5py is None:
            print("TensorFlow or h5py not available, skipping HDF5 benchmark")
            return None

        results = {}
        for size_name, size_mb in [('small', 500), ('large', 2000)]:
            file_path = self.test_dir / f"tf_h5_{size_name}_{size_mb}mb.h5"
            file_size = size_mb * 1024 * 1024

            def write_func():
                total_data_size = 0
                with h5py.File(file_path, "w") as f:
                    num_layers = 8
                    target_data_size = file_size
                    data_per_dataset = target_data_size // (num_layers * 2)
                    for i in range(num_layers):
                        weights_elements = data_per_dataset // 4
                        weights_data = np.random.randn(weights_elements).astype(np.float32)
                        f.create_dataset(f'conv_{i}_weights', data=weights_data)
                        total_data_size += weights_data.nbytes
                        bias_data = np.random.randn(256).astype(np.float32)
                        f.create_dataset(f'conv_{i}_bias', data=bias_data)
                        total_data_size += bias_data.nbytes

                actual_size = os.path.getsize(file_path)
                return {"file_size": actual_size, "num_datasets": num_layers * 2}

            def read_func():
                total_size = 0
                dataset_count = 0
                data_checksum = 0
                actual_size = os.path.getsize(file_path)
                with h5py.File(file_path, "r") as f:
                    for key in f.keys():
                        if isinstance(f[key], h5py.Dataset):
                            # 实际读取数据
                            data = f[key][:]
                            total_size += data.nbytes
                            dataset_count += 1
                            # 处理数据确保实际读取
                            data_checksum = (data_checksum + np.sum(data)) % 1000000
                return {"file_size": actual_size, "num_datasets": dataset_count}
            write_stats = self.run_benchmark(
                f"tensorflow_h5_{size_name}_write", write_func, file_size=file_size,
                description=f"Write TensorFlow H5 ({size_mb}MB)"
            )
            self.clear_cache()
            read_stats = self.run_benchmark(
                f"tensorflow_h5_{size_name}_read", read_func, file_size=file_size,
                description=f"Read TensorFlow H5 ({size_mb}MB)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_onnx(self):
        """Benchmark ONNX model format"""
        if onnx is None or ort is None:
            print("ONNX or ONNX Runtime not available, skipping ONNX benchmark")
            return None

        results = {}
        for size_name, size_mb in [('small', 50), ('medium', 100), ('large', 200)]:
            file_path = self.test_dir / f"onnx_model_{size_name}_{size_mb}mb.onnx"
            file_size = size_mb * 1024 * 1024

            # Create a simple ONNX model
            def create_onnx_model():
                from onnx import helper, TensorProto, save

                # Calculate appropriate tensor sizes to match target file size
                tensor_size = max(100, int((file_size * 0.8) / 4 / 4))  # Rough estimation

                # Create a simple graph
                X = helper.make_tensor_value_info('X', TensorProto.FLOAT, [1, 3, 224, 224])
                Y = helper.make_tensor_value_info('Y', TensorProto.FLOAT, [1, 1000])

                # Create weights with appropriate size
                weights = helper.make_tensor(
                    'W',
                    TensorProto.FLOAT,
                    [3 * 224 * 224, 1000],
                    np.random.randn(3 * 224 * 224 * 1000).astype(np.float32)[:3*224*224*1000]
                )

                node = helper.make_node(
                    'MatMul',
                    ['X', 'W'],
                    ['Y'],
                    name='matmul'
                )

                graph = helper.make_graph(
                    [node],
                    'simple_model',
                    [X],
                    [Y],
                    [weights]
                )

                model = helper.make_model(graph, producer_name='benchmark')
                return model

            def write_func():
                model = create_onnx_model()
                onnx.save(model, file_path)
                actual_size = os.path.getsize(file_path)
                return {"file_size": actual_size, "model_size": file_size}

            def read_func():
                # Load and validate model
                model = onnx.load(file_path)
                onnx.checker.check_model(model)

                # Run inference with ONNX Runtime
                sess = ort.InferenceSession(file_path)
                input_data = np.random.randn(1, 3, 224, 224).astype(np.float32)
                outputs = sess.run(None, {'X': input_data})
                return {"output_shape": outputs[0].shape, "model_valid": True}

            write_stats = self.run_benchmark(
                f"onnx_{size_name}_write", write_func, file_size=file_size,
                description=f"Write ONNX model ({size_mb}MB)"
            )

            read_stats = self.run_benchmark(
                f"onnx_{size_name}_read", read_func, file_size=file_size,
                description=f"Read ONNX model ({size_mb}MB)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_huggingface_bin(self):
        """Benchmark HuggingFace .bin format"""
        if torch is None:
            print("PyTorch not available, skipping HuggingFace benchmark")
            return None

        results = {}
        for size_name, size_mb in [('small', 10), ('medium', 50), ('large', 100)]:
            file_path = self.test_dir / f"hf_model_{size_name}_{size_mb}mb.bin"
            file_size = size_mb * 1024 * 1024

            # Create HuggingFace-style model weights
            def create_hf_weights():
                # Calculate layer sizes to approximate target file size
                num_layers = 12
                layer_size = max(100, int((file_size * 0.9) / num_layers / 4))  # Rough estimation

                weights = {}
                for i in range(num_layers):
                    weights[f"layer.{i}.attention.self.query.weight"] = torch.randn(layer_size)
                    weights[f"layer.{i}.attention.self.key.weight"] = torch.randn(layer_size)
                    weights[f"layer.{i}.attention.self.value.weight"] = torch.randn(layer_size)
                    weights[f"layer.{i}.attention.output.dense.weight"] = torch.randn(layer_size)
                    weights[f"layer.{i}.intermediate.dense.weight"] = torch.randn(layer_size)
                    weights[f"layer.{i}.output.dense.weight"] = torch.randn(layer_size)

                # Add embeddings
                weights["embeddings.word_embeddings.weight"] = torch.randn(layer_size)
                weights["embeddings.position_embeddings.weight"] = torch.randn(512, layer_size)
                weights["embeddings.token_type_embeddings.weight"] = torch.randn(2, layer_size)

                return weights

            def write_func():
                weights = create_hf_weights()
                torch.save(weights, file_path)
                actual_size = os.path.getsize(file_path)
                return {"file_size": actual_size, "num_tensors": len(weights)}

            def read_func():
                weights = torch.load(file_path)
                total_params = sum(param.numel() for param in weights.values())
                return {"loaded_params": total_params, "num_tensors": len(weights)}

            write_stats = self.run_benchmark(
                f"huggingface_{size_name}_write", write_func, file_size=file_size,
                description=f"Write HuggingFace weights ({size_mb}MB)"
            )

            read_stats = self.run_benchmark(
                f"huggingface_{size_name}_read", read_func, file_size=file_size,
                description=f"Read HuggingFace weights ({size_mb}MB)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_tensorflow_checkpoint(self):
        """Benchmark TensorFlow checkpoint format"""
        if tf is None:
            print("TensorFlow not available, skipping TF checkpoint benchmark")
            return None
     #   physical_devices = tf.config.list_physical_devices('CPU')
     #   if physical_devices:
     #       try:
     #           tf.config.set_logical_device_configuration(
     #               physical_devices[0],
     #               [tf.config.LogicalDeviceConfiguration(memory_limit=5 * 1024)]  # 5GB
     #           )
     #           print("Set TensorFlow memory limit to 5GB")
     #       except RuntimeError as e:
     #           print(f"Could not set memory limit: {e}")
        results = {}
        for size_name, size_mb in [('small', 10), ('large', 100)]:
            checkpoint_dir = self.test_dir / f"tf_checkpoint_{size_name}_{size_mb}mb"
            checkpoint_dir.mkdir(exist_ok=True)
            file_size = size_mb * 1024 * 1024

            def write_func():
                # Create a simple model
                if size_mb == 10:
                    layer_sizes = [2048, 1024, 512, 256, 128, 64]
                else:  # 100MB
                    layer_sizes = [8192, 4096, 2048, 1024, 512, 256]
                layers = [tf.keras.layers.Dense(layer_sizes[0], activation='relu', input_shape=(784,))]
                for size in layer_sizes[1:]:
                    layers.append(tf.keras.layers.Dense(size, activation='relu'))
                layers.append(tf.keras.layers.Dense(10, activation='softmax'))
                model = tf.keras.Sequential(layers)
                checkpoint = tf.train.Checkpoint(model=model)
                checkpoint_path = checkpoint_dir / "model.ckpt"
                checkpoint.write(str(checkpoint_path))
                del model
                tf.keras.backend.clear_session()
            
                total_size = sum(file.stat().st_size for file in checkpoint_dir.glob("*"))
                return {"file_size": total_size, "num_files": len(list(checkpoint_dir.glob("*")))}

            def read_func():
                if size_mb == 10:
                    layer_sizes = [2048, 1024, 512, 256, 128, 64]
                else:
                    layer_sizes = [8192, 4096, 2048, 1024, 512, 256]
            
                layers = [tf.keras.layers.Dense(layer_sizes[0], activation='relu', input_shape=(784,))]
                for size in layer_sizes[1:]:
                    layers.append(tf.keras.layers.Dense(size, activation='relu'))
                layers.append(tf.keras.layers.Dense(10, activation='softmax'))
            
                model = tf.keras.Sequential(layers)
                checkpoint = tf.train.Checkpoint(model=model)
                checkpoint_path = checkpoint_dir / "model.ckpt"
                checkpoint.restore(str(checkpoint_path))

                batch_size = 64
                num_batches = 100
            
                for i in range(num_batches):
                    test_input = tf.random.normal((batch_size, 784))
                    output = model(test_input)
                    _ = tf.reduce_mean(output)
            
                del model
                tf.keras.backend.clear_session()
            
                return {"output_shape": output.shape, "restored": True}
            tf.keras.backend.clear_session()
            import gc
            gc.collect()
            write_stats = self.run_benchmark(
                f"tf_checkpoint_{size_name}_write", write_func, file_size=file_size,
                description=f"Write TF checkpoint ({size_mb}MB)"
            )

            read_stats = self.run_benchmark(
                f"tf_checkpoint_{size_name}_read", read_func, file_size=file_size,
                description=f"Read TF checkpoint ({size_mb}MB)"
            )

            # Cleanup
            if checkpoint_dir.exists():
                shutil.rmtree(checkpoint_dir)

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    # ----------------------------------------------------------------------
    # Dataset Format Benchmarks
    # ----------------------------------------------------------------------

    def benchmark_tfrecord(self):
        """Benchmark TFRecord format for datasets"""
        if tf is None:
            print("TensorFlow not available, skipping TFRecord benchmark")
            return None

        results = {}
        num_samples = self.config['num_samples']
        image_size = self.config['image_size']
        
        for size_name, sample_multiplier in [('small', 1), ('medium', 2), ('large', 4)]:
            actual_samples = num_samples * sample_multiplier
            file_path = self.test_dir / f"tfrecord_{size_name}_{actual_samples}samples.tfrecord"
        
            image_data_size = np.prod(image_size) * 4  # 图像数据大小 (float32)
            sample_size_estimate = image_data_size + 100  # 图像 + 标签 + 元数据
            file_size_bytes = actual_samples * sample_size_estimate

            def create_example(image_data, label, extra_features):
                feature = {
                    'image': tf.train.Feature(
                        bytes_list=tf.train.BytesList(value=[image_data])),
                    'label': tf.train.Feature(
                        int64_list=tf.train.Int64List(value=[label])),
                    'extra_features': tf.train.Feature(
                        float_list=tf.train.FloatList(value=extra_features))
                }
                return tf.train.Example(features=tf.train.Features(feature=feature))

            def write_func():
                with tf.io.TFRecordWriter(str(file_path)) as writer:
                    for i in range(actual_samples):
                    # 创建随机图像数据和额外特征
                        image_data = np.random.rand(*image_size).astype(np.float32).tobytes()
                        label = i % 100
                        extra_features = np.random.randn(10).astype(np.float32).tolist()

                        example = create_example(image_data, label, extra_features)
                        writer.write(example.SerializeToString())

                actual_file_size = os.path.getsize(file_path)
                return {"file_size": actual_file_size, "num_samples": actual_samples}
        
            def read_func():
                def parse_example(example_proto):
                    feature_description = {
                        'image': tf.io.FixedLenFeature([], tf.string),
                        'label': tf.io.FixedLenFeature([], tf.int64),
                        'extra_features': tf.io.FixedLenFeature([10], tf.float32),
                    }
                    return tf.io.parse_single_example(example_proto, feature_description)

            # 创建数据集
                dataset = tf.data.TFRecordDataset(str(file_path))
                dataset = dataset.map(parse_example)

            # 实际读取和处理所有样本
                total_samples = 0
                total_image_size = 0
                label_sum = 0
                feature_sum = 0.0
                for example in dataset:
                    total_samples += 1

                # 实际处理图像数据（触发磁盘读取）
                    image_data = tf.io.decode_raw(example['image'], tf.float32)
                    total_image_size += image_data.shape[0] * 4  # 4 bytes per float32

                # 处理标签和特征数据
                    label_sum += example['label'].numpy()
                    feature_sum += tf.reduce_sum(example['extra_features']).numpy()

            # 验证处理结果（防止编译器优化）
                validation_value = (label_sum + int(feature_sum)) % 1000
                _ = validation_value  # 确保值被使用

                file_size = os.path.getsize(file_path)
                return {
                    "samples_read": total_samples,
                    "file_size": file_size,
                    "total_data_processed": total_image_size,
                    "validation_ok": validation_value >= 0
                }

            write_stats = self.run_benchmark(
                f"tfrecord_{size_name}_write", write_func, file_size=file_size_bytes,
                description=f"Write TFRecord ({actual_samples} samples)"
            )

            if write_stats:
                self.clear_cache()
                # 读取测试
                read_stats = self.run_benchmark(
                    f"tfrecord_{size_name}_read", read_func, file_size=file_size_bytes,
                    description=f"Read TFRecord ({actual_samples} samples)"
                )

            # 清理文件
                if file_path.exists():
                    file_path.unlink()

                if read_stats:
                    results[size_name] = {"write": write_stats, "read": read_stats}
            elif file_path.exists():
                file_path.unlink()
        return results    

    def benchmark_hdf5_dataset(self):
        """Benchmark HDF5 format for datasets"""
        if h5py is None:
            print("h5py not available, skipping HDF5 dataset benchmark")
            return None

        results = {}
        num_samples = self.config['num_samples']
        image_size = self.config['image_size']

        sample_size_estimate = np.prod(image_size) * 4 

        for size_name, sample_multiplier in [('small', 1), ('medium', 2)]:
            actual_samples = num_samples * sample_multiplier
            file_path = self.test_dir / f"hdf5_dataset_{size_name}_{actual_samples}samples.h5"
            file_size_bytes = actual_samples * sample_size_estimate

            def write_func():
                all_images = np.random.rand(actual_samples, *image_size).astype(np.float32)
                all_labels = np.arange(actual_samples) % 10

                with h5py.File(file_path, 'w') as f:
                    images = f.create_dataset(
                        'images',
                        data=all_images,
                        dtype=np.float32,
                        compression='gzip'
                    )
                    labels = f.create_dataset(
                        'labels',
                        data=all_labels,
                        dtype=np.int64
                    )

                actual_file_size = os.path.getsize(file_path)
                return {"file_size": actual_file_size, "num_samples": actual_samples}
            def read_func():
                with h5py.File(file_path, 'r') as f:
                    images = f['images'][:]
                    labels = f['labels'][:]

                total_images = len(images)
                file_size = os.path.getsize(file_path)
                return {"samples_read": total_images, "file_size": file_size}

            write_stats = self.run_benchmark(
                f"hdf5_dataset_{size_name}_write", write_func, file_size=file_size_bytes,
                description=f"Write HDF5 dataset ({actual_samples} samples)"
            )
            self.clear_cache()
            read_stats = self.run_benchmark(
                f"hdf5_dataset_{size_name}_read", read_func, file_size=file_size_bytes,
                description=f"Read HDF5 dataset ({actual_samples} samples)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_parquet(self):
        """Benchmark Parquet format for datasets"""
        if pq is None or pa is None:
            print("PyArrow not available, skipping Parquet benchmark")
            return None

        results = {}
        num_samples = self.config['num_samples']

        sample_size_estimate = 500

        for size_name, sample_multiplier in [('small', 2), ('medium', 4), ('large', 8)]:
            actual_samples = num_samples * sample_multiplier
            file_path = self.test_dir / f"parquet_{size_name}_{actual_samples}samples.parquet"
            file_size_bytes = actual_samples * sample_size_estimate

            def write_func():
                # Create sample data
                data = {
                    'id': list(range(actual_samples)),
                    'feature1': np.random.randn(actual_samples).astype(np.float32),
                    'feature2': np.random.randn(actual_samples).astype(np.float32),
                    'feature3': np.random.randn(actual_samples).astype(np.float32),
                    'label': np.random.randint(0, 10, actual_samples).astype(np.int64),
                    'timestamp': [time.time()] * actual_samples
                }

                table = pa.Table.from_pydict(data)
                pq.write_table(table, file_path, compression='snappy')

                actual_file_size = os.path.getsize(file_path)
                return {"file_size": actual_file_size, "num_samples": actual_samples}
           
            def read_func():
                parquet_file = pq.ParquetFile(file_path)
                total_rows = 0
                feature_sum = 0.0
            
                for i in range(parquet_file.num_row_groups):
                    table = parquet_file.read_row_group(i)
                    df = table.to_pandas()
                    total_rows += len(df)
                    feature_sum += df['feature1'].sum() + df['feature2'].sum()
            
                _ = feature_sum % 1000
                return {"rows_read": total_rows, "file_size": os.path.getsize(file_path)}

            write_stats = self.run_benchmark(
                f"parquet_{size_name}_write", write_func, file_size=file_size_bytes,
                description=f"Write Parquet ({actual_samples} samples)"
            )
            self.clear_cache()
            read_stats = self.run_benchmark(
                f"parquet_{size_name}_read", read_func, file_size=file_size_bytes,
                description=f"Read Parquet ({actual_samples} samples)"
            )

            if file_path.exists():
                file_path.unlink()

            if write_stats and read_stats:
                results[size_name] = {"write": write_stats, "read": read_stats}

        return results

    def benchmark_comprehensive(self):
        """Run comprehensive benchmarks with multiple file sizes"""
        benchmarks = [
            ("LMDB", self.benchmark_lmdb),
            ("PyTorch Weights", self.benchmark_pytorch_weights),
            ("TensorFlow H5", self.benchmark_tensorflow_h5),
        #    ("ONNX", self.benchmark_onnx),
            ("HuggingFace Bin", self.benchmark_huggingface_bin),
            ("TensorFlow Checkpoint", self.benchmark_tensorflow_checkpoint),
        #    ("TFRecord Dataset", self.benchmark_tfrecord),
            ("HDF5 Dataset", self.benchmark_hdf5_dataset),
            ("Parquet Dataset", self.benchmark_parquet),
        ]

        comprehensive_results = {}

        for name, benchmark_func in benchmarks:
            try:
                print(f"\n{'='*60}")
                print(f"RUNNING COMPREHENSIVE {name.upper()} BENCHMARK")
                print(f"{'='*60}")

                result = benchmark_func()
                if result:
                    comprehensive_results[name.lower().replace(" ", "_")] = result
                    print(f"✓ Completed comprehensive {name} benchmark")
                else:
                    print(f"✗ {name} benchmark returned no results")

            except Exception as e:
                print(f"✗ Error running comprehensive {name} benchmark: {e}")
                import traceback
                traceback.print_exc()
                comprehensive_results[name.lower().replace(" ", "_")] = {"error": str(e)}

        return comprehensive_results

    def generate_report(self):
        """Generate detailed performance report"""
        def default_serializer(obj):
            if isinstance(obj, BenchmarkResult):
                return {
                    'min_time': obj.min_time,
                    'max_time': obj.max_time,
                    'mean_time': obj.mean_time,
                    'std_time': obj.std_time,
                    'throughput_mb_s': obj.throughput_mb_s,
                    'file_size_bytes': obj.file_size_bytes,
                    'operation_count': obj.operation_count,
                    'details': obj.details
                }
            elif isinstance(obj, np.integer):
                return int(obj)
            elif isinstance(obj, np.floating):
                return float(obj)
            elif isinstance(obj, np.ndarray):
                return obj.tolist()
            elif hasattr(obj, '__dict__'):
                return obj.__dict__
            return str(obj)

        report = {
            'version': self.version,
            'timestamp': time.strftime('%Y-%m-%d %H:%M:%S'),
            'mount_point': str(self.mount_point),
            'config': self.config,
            'environment': {
                'python_version': sys.version,
                'has_torch': torch is not None,
                'has_tensorflow': tf is not None,
                'has_h5py': h5py is not None,
                'has_pyarrow': pq is not None,
                'has_onnx': onnx is not None,
                'has_onnxruntime': ort is not None
            },
            'results': self.results
        }

        summary = {}
        for benchmark_name, benchmark_data in self.results.items():
            if isinstance(benchmark_data, dict) and 'error' not in benchmark_data:
                for size_name, size_data in benchmark_data.items():
                    if isinstance(size_data, dict):
                        for op_type, stats in size_data.items():
                            if hasattr(stats, 'throughput_mb_s') and stats.throughput_mb_s:
                                key = f"{benchmark_name}_{size_name}_{op_type}"
                                summary[key] = {
                                    'throughput_mb_s': stats.throughput_mb_s,
                                    'time_s': stats.mean_time,
                                    'file_size_mb': stats.file_size_bytes / (1024**2) if stats.file_size_bytes else None
                                }

        report['summary'] = summary
        return report

    def save_results(self):
        """Save results to JSON file with comprehensive report"""
        self.results_file.parent.mkdir(parents=True, exist_ok=True)

        report = self.generate_report()

        def default_serializer(obj):
            if isinstance(obj, BenchmarkResult):
                return {
                    'min_time': obj.min_time,
                    'max_time': obj.max_time,
                    'mean_time': obj.mean_time,
                    'std_time': obj.std_time,
                    'throughput_mb_s': obj.throughput_mb_s,
                    'file_size_bytes': obj.file_size_bytes,
                    'operation_count': obj.operation_count,
                    'details': obj.details
                }
            elif isinstance(obj, np.integer):
                return int(obj)
            elif isinstance(obj, np.floating):
                return float(obj)
            elif isinstance(obj, np.ndarray):
                return obj.tolist()
            elif hasattr(obj, '__dict__'):
                return obj.__dict__
            return str(obj)

        with open(self.results_file, 'w') as f:
            json.dump(report, f, indent=2, default=default_serializer)

        print(f"\nResults saved to {self.results_file}")

    def print_detailed_summary(self):
        """Print detailed summary of all benchmark results"""
        print(f"\n{'='*80}")
        print(f"COMPREHENSIVE AI FORMAT PERFORMANCE BENCHMARK SUMMARY")
        print(f"Version: {self.version}")
        print(f"Timestamp: {time.strftime('%Y-%m-%d %H:%M:%S')}")
        print(f"{'='*80}")

        for benchmark_name, benchmark_data in self.results.items():
            if isinstance(benchmark_data, dict) and 'error' in benchmark_data:
                print(f"\n{benchmark_name.upper()}: ERROR - {benchmark_data['error']}")
                continue

            print(f"\n{benchmark_name.upper()}:")
            for size_name, size_data in benchmark_data.items():
                print(f"  {size_name.upper()} FILES:")
                for op_type, stats in size_data.items():
                    if hasattr(stats, 'mean_time'):
                        print(f"    {op_type.upper()}:")
                        print(f"      Time:      {stats.mean_time:.3f}s ± {stats.std_time:.3f}s")
                        print(f"      Range:     {stats.min_time:.3f}s - {stats.max_time:.3f}s")

                        if stats.throughput_mb_s:
                            print(f"      Throughput: {stats.throughput_mb_s:.2f} MB/s")

                        if stats.file_size_bytes:
                            size_mb = stats.file_size_bytes / (1024**2)
                            print(f"      Size:      {size_mb:.1f} MB")

                        if stats.details:
                            details = ", ".join([f"{k}: {v}" for k, v in stats.details.items()])
                            print(f"      Details:    {details}")

def main():
    parser = argparse.ArgumentParser(description="Comprehensive AI Format Performance Benchmark")
    parser.add_argument("mount_point", help="Mount point to test")
    parser.add_argument("results_file", help="File to save results JSON")
    parser.add_argument("version", help="Version identifier")
    parser.add_argument("--verbose", "-v", action="store_true", help="Verbose output")
    parser.add_argument("--quick", "-q", action="store_true", help="Quick test (small files only)")
    args = parser.parse_args()

    benchmark = AIFormatBenchmark(args.mount_point, args.results_file, args.version)
    benchmark.verbose = args.verbose

    if args.quick:
        benchmark.config.update({
            'small_file_mb': 10,
            'medium_file_mb': 20,
            'large_file_mb': 50,
            'num_runs': 1,
            'cool_down_time': 0.3,
            'num_samples': 500,
            'lmdb_num_samples': 200,
            'lmdb_num_proc': 1,
            'lmdb_image_size': (32, 32)
        })

    print("Starting Comprehensive AI Format Performance Benchmark...")
    print(f"Configuration: {benchmark.config}")

    # Run comprehensive benchmarks
    benchmark.results = benchmark.benchmark_comprehensive()

    # Save and display results
    benchmark.save_results()
    benchmark.print_detailed_summary()

if __name__ == "__main__":
    main()
