# -*- coding: utf-8 -*-
import time
import os
import random
import string
import sys
from threading import Thread

def random_string(size):
    return ''.join(random.choices(string.ascii_letters + string.digits, k=size))

def writer(filepath, duration_minutes=5):
    start_time = time.time()
    end_time = start_time + duration_minutes * 60

    while time.time() < end_time:
        # Generate random size between 1MB and 128MB (adjust range as needed)
        min_size = 1 * 1024 * 1024  # 1MB
        max_size = 128 * 1024 * 1024  # 128MB
        random_size = random.randint(min_size, max_size)

        data = random_string(random_size).encode('utf-8')

        with open(filepath, 'ab') as f:
            f.write(data)

        # Optional: print the size written
        print(f"[Writer] Written {random_size} bytes ({(random_size/1024/1024):.2f} MB)")

    print(f"[Writer] Test duration ({duration_minutes} minutes) completed, stopping writer.")

def watch_file(path1="/jfs/tmp.txt", path2="/jfs2/tmp.txt", interval=0.01, duration_minutes=5):
    """
    Detect the change in file length every interval second (default 0.01 seconds, that is, 10ms).
    If there is any new content, read the new part and compare whether the corresponding parts of the two files are consistent.
    """
    start_time = time.time()
    end_time = start_time + duration_minutes * 60

    try:
        with open(path2, "rb") as f2:
            f2.seek(0, 2)
            last_pos = f2.tell()
    except FileNotFoundError:
        print(f"[Watcher] {path2} no exist，create it...")
        last_pos = 0

    with open(path2, "rb") as f2:
        while time.time() < end_time:
            time.sleep(interval)
            try:
                os.listdir('/jfs2')
                f2.seek(0, 2)
                curr_size = f2.tell()

                if curr_size > last_pos:
                    f2.seek(last_pos)
                    data2 = f2.read(curr_size - last_pos)
                    last_pos = curr_size

                    try:
                        with open(path1, "rb") as f1:
                            f1.seek(last_pos - len(data2))
                            data1 = f1.read(len(data2))

                            if data1 != data2:
                                print(f"[Watcher] The differences are found in location {last_pos - len(data2)}-{last_pos}:")
                                diff_file1 = f"data1"
                                diff_file2 = f"data2"

                                with open(diff_file1, "wb") as df1:
                                    df1.write(data1)
                                with open(diff_file2, "wb") as df2:
                                    df2.write(data2)
                                print(f"[Watcher] The differences write to：{diff_file1} 和 {diff_file2}")
                                sys.exit(1)
                            else:
                                print(f"[Watcher] The newly added content is consistent. ({last_pos - len(data2)}-{last_pos})")
                    except FileNotFoundError:
                        print(f"[Watcher] file {path1} not exist")
                        continue

            except FileNotFoundError:
                last_pos = 0
                continue

    print(f"[Watcher] Test duration ({duration_minutes} minutes) completed, no differences found.")

def main():
    test_duration_minutes = 5
    filepath1 = '/jfs/tmp.txt'
    filepath2 = '/jfs2/tmp.txt'

    for path in [filepath1, filepath2]:
        try:
            with open(path, 'wb') as f:
                f.write(b'')
        except IOError as e:
            sys.exit(1)

    writer_thread = Thread(target=writer, args=(filepath1, test_duration_minutes))
    writer_thread.daemon = True
    writer_thread.start()

    watch_file(filepath1, filepath2, duration_minutes=test_duration_minutes)

    writer_thread.join()

    print("test finally")

if __name__ == '__main__':
    main()
