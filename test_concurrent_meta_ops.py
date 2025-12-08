#!/usr/bin/env python3
"""
JuiceFS 并发 Meta 请求测试脚本（Python 版本）
用于复现多个进程同时访问同一挂载点时的重复 meta 请求问题

使用方法:
    python3 test_concurrent_meta_ops.py <mount_point> [num_processes] [test_file]
    
示例:
    python3 test_concurrent_meta_ops.py /mnt/jfs 20 test_file.txt
"""

import os
import sys
import time
import multiprocessing
import argparse
from pathlib import Path
from concurrent.futures import ProcessPoolExecutor, as_completed
import subprocess


def do_meta_operations(mount_point, test_file, test_dir, process_id, iterations=100):
    """
    执行元数据操作：getattr 和 lookup
    """
    test_path = os.path.join(mount_point, test_file)
    results = {
        'process_id': process_id,
        'getattr_count': 0,
        'lookup_count': 0,
        'errors': 0,
        'start_time': time.time(),
    }
    
    try:
        for i in range(iterations):
            # 1. GetAttr 操作 - 获取文件属性
            try:
                os.stat(test_path)
                results['getattr_count'] += 1
            except Exception as e:
                results['errors'] += 1
                print(f"进程 {process_id} GetAttr 错误: {e}", file=sys.stderr)
            
            # 2. GetAttr 操作 - 获取目录属性
            try:
                os.stat(test_dir)
                results['getattr_count'] += 1
            except Exception as e:
                results['errors'] += 1
            
            # 3. Lookup 操作 - 查找文件
            try:
                test_file_path = os.path.join(test_dir, "file_1.txt")
                os.stat(test_file_path)
                results['lookup_count'] += 1
            except Exception as e:
                results['errors'] += 1
            
            # 4. Lookup 操作 - 列出目录内容（触发多个 lookup）
            try:
                entries = os.listdir(test_dir)
                results['lookup_count'] += len(entries)
            except Exception as e:
                results['errors'] += 1
            
            # 5. 嵌套路径访问（触发多个 lookup）
            try:
                nested_path = os.path.join(test_dir, "file_2.txt")
                if os.path.exists(nested_path):
                    os.stat(nested_path)
                    results['lookup_count'] += 1
            except Exception as e:
                results['errors'] += 1
        
        results['end_time'] = time.time()
        results['duration'] = results['end_time'] - results['start_time']
        
    except Exception as e:
        results['errors'] += 1
        print(f"进程 {process_id} 发生错误: {e}", file=sys.stderr)
    
    return results


def analyze_access_log(access_log_path):
    """
    分析访问日志，统计 getattr 和 lookup 操作
    """
    if not os.path.exists(access_log_path):
        print(f"警告: 访问日志不存在: {access_log_path}")
        return
    
    print("\n" + "="*60)
    print("访问日志分析")
    print("="*60)
    
    # 统计操作类型
    cmd = f"grep -E '(getattr|lookup)' {access_log_path} 2>/dev/null | wc -l"
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    total_ops = result.stdout.strip()
    print(f"总 getattr/lookup 操作数: {total_ops}")
    
    # 统计 getattr
    cmd = f"grep 'getattr' {access_log_path} 2>/dev/null | wc -l"
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    getattr_count = result.stdout.strip()
    print(f"GetAttr 操作数: {getattr_count}")
    
    # 统计 lookup
    cmd = f"grep 'lookup' {access_log_path} 2>/dev/null | wc -l"
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    lookup_count = result.stdout.strip()
    print(f"Lookup 操作数: {lookup_count}")
    
    # 找出最频繁访问的 inode（通过 getattr 参数）
    print("\n最频繁访问的 inode (GetAttr):")
    cmd = f"grep 'getattr' {access_log_path} 2>/dev/null | " \
          f"grep -oP '\\(\\K[0-9]+' | sort | uniq -c | sort -rn | head -10"
    subprocess.run(cmd, shell=True)
    
    # 找出最频繁查找的 entry（通过 lookup 参数）
    print("\n最频繁查找的 entry (Lookup):")
    cmd = f"grep 'lookup' {access_log_path} 2>/dev/null | " \
          f"grep -oP '\\([0-9]+,\\K[^)]+' | sort | uniq -c | sort -rn | head -10"
    subprocess.run(cmd, shell=True)
    
    # 统计并发请求（相同时间戳的操作）
    print("\n时间窗口内的并发请求分析:")
    print("（查看是否有多个进程在同一时间访问相同的 inode/entry）")
    cmd = f"grep -E '(getattr|lookup)' {access_log_path} 2>/dev/null | " \
          f"head -50 | cut -d' ' -f1-2"
    subprocess.run(cmd, shell=True)


def main():
    parser = argparse.ArgumentParser(
        description='JuiceFS 并发 Meta 请求测试脚本',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  # 使用默认参数（10个进程，100次迭代）
  python3 test_concurrent_meta_ops.py /mnt/jfs
  
  # 指定进程数和迭代次数
  python3 test_concurrent_meta_ops.py /mnt/jfs --processes 20 --iterations 200
  
  # 启用详细输出和分析
  python3 test_concurrent_meta_ops.py /mnt/jfs --processes 20 --analyze
        """
    )
    
    parser.add_argument('mount_point', nargs='?', default='/mnt/jfs',
                       help='JuiceFS 挂载点路径 (默认: /mnt/jfs)')
    parser.add_argument('--processes', '-p', type=int, default=10,
                       help='并发进程数 (默认: 10)')
    parser.add_argument('--iterations', '-i', type=int, default=100,
                       help='每个进程的迭代次数 (默认: 100)')
    parser.add_argument('--test-file', '-f', default='test_file.txt',
                       help='测试文件名 (默认: test_file.txt)')
    parser.add_argument('--analyze', '-a', action='store_true',
                       help='测试后自动分析访问日志')
    parser.add_argument('--no-cleanup', action='store_true',
                       help='测试后不清理测试文件')
    
    args = parser.parse_args()
    
    mount_point = args.mount_point
    num_processes = args.processes
    iterations = args.iterations
    test_file = args.test_file
    
    print("="*60)
    print("JuiceFS 并发 Meta 请求测试")
    print("="*60)
    print(f"挂载点: {mount_point}")
    print(f"并发进程数: {num_processes}")
    print(f"每进程迭代次数: {iterations}")
    print(f"测试文件: {test_file}")
    print("="*60)
    print()
    
    # 检查挂载点
    if not os.path.isdir(mount_point):
        print(f"错误: 挂载点不存在: {mount_point}")
        sys.exit(1)
    
    # 创建测试文件
    test_path = os.path.join(mount_point, test_file)
    if not os.path.exists(test_path):
        print(f"创建测试文件: {test_path}")
        with open(test_path, 'w') as f:
            f.write("This is a test file for concurrent meta operations\n")
    
    # 创建测试目录
    test_dir = os.path.join(mount_point, f"test_dir_{os.getpid()}")
    if not os.path.exists(test_dir):
        print(f"创建测试目录: {test_dir}")
        os.makedirs(test_dir, exist_ok=True)
        
        # 在测试目录中创建一些文件
        for i in range(1, 6):
            file_path = os.path.join(test_dir, f"file_{i}.txt")
            with open(file_path, 'w') as f:
                f.write(f"test content {i}\n")
    
    access_log_path = os.path.join(mount_point, ".accesslog")
    
    print()
    print("开始并发测试...")
    print(f"开始时间: {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print()
    
    start_time = time.time()
    
    # 使用进程池执行并发操作
    with ProcessPoolExecutor(max_workers=num_processes) as executor:
        futures = []
        for i in range(1, num_processes + 1):
            future = executor.submit(
                do_meta_operations,
                mount_point, test_file, test_dir, i, iterations
            )
            futures.append(future)
        
        # 收集结果
        results = []
        completed = 0
        for future in as_completed(futures):
            result = future.result()
            results.append(result)
            completed += 1
            if completed % 5 == 0:
                print(f"已完成 {completed}/{num_processes} 个进程...")
    
    end_time = time.time()
    total_duration = end_time - start_time
    
    print()
    print("="*60)
    print("测试完成")
    print("="*60)
    print(f"总耗时: {total_duration:.2f} 秒")
    print()
    
    # 统计结果
    total_getattr = sum(r['getattr_count'] for r in results)
    total_lookup = sum(r['lookup_count'] for r in results)
    total_errors = sum(r['errors'] for r in results)
    
    print("操作统计:")
    print(f"  总 GetAttr 操作: {total_getattr}")
    print(f"  总 Lookup 操作: {total_lookup}")
    print(f"  总错误数: {total_errors}")
    print(f"  平均每进程 GetAttr: {total_getattr / num_processes:.1f}")
    print(f"  平均每进程 Lookup: {total_lookup / num_processes:.1f}")
    print()
    
    # 分析访问日志
    if args.analyze and os.path.exists(access_log_path):
        analyze_access_log(access_log_path)
    
    print()
    print("="*60)
    print("观测方法")
    print("="*60)
    print()
    print("1. 实时查看访问日志:")
    print(f"   tail -f {access_log_path} | grep -E '(getattr|lookup)'")
    print()
    print("2. 使用 juicefs profile 分析:")
    print(f"   cat {access_log_path} > /tmp/jfs_test.alog")
    print(f"   juicefs profile /tmp/jfs_test.alog --interval 0")
    print()
    print("3. 使用 juicefs stats 实时监控（在另一个终端）:")
    print(f"   juicefs stats {mount_point} -l 1")
    print()
    print("4. 查看 Prometheus metrics（如果启用）:")
    print("   curl http://localhost:9567/metrics | grep meta_ops")
    print()
    print("5. 分析重复请求（相同 inode 的并发请求）:")
    print(f"   grep -E '(getattr|lookup)' {access_log_path} | \\")
    print("     awk '{print $NF}' | sort | uniq -c | sort -rn | head -20")
    print()
    
    # 清理
    if not args.no_cleanup:
        print("清理测试文件...")
        try:
            if os.path.exists(test_dir):
                import shutil
                shutil.rmtree(test_dir)
            print(f"已清理: {test_dir}")
        except Exception as e:
            print(f"清理失败: {e}")
    
    print()
    print("="*60)


if __name__ == '__main__':
    main()

