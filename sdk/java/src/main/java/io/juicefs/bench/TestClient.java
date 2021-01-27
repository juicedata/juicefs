/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package io.juicefs.bench;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FSDataOutputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;

public class TestClient {
  final static ThreadLocalRandom random = ThreadLocalRandom.current();
  final static Logger LOG = LoggerFactory.getLogger(TestClient.class);

  enum TestType {
    READ, WRITE
  }

  public static void main(String[] args) throws Exception, InterruptedException {
    String path_string = null;
    int threads = 1;
    int bufferSize = 1000000;
    long fileSize = 1;
    TestType type = null;

    for (int i = 0; i < args.length; i++) {
      if ("-write".equals(args[i])) {
        type = TestType.WRITE;
      } else if ("-read".equals(args[i])) {
        type = TestType.READ;
      } else if ("-path".equals(args[i])) {
        path_string = args[++i];
      } else if ("-threads".equals(args[i])) {
        threads = Integer.parseInt(args[++i]);
      } else if ("-bufferSize".equals(args[i])) {
        bufferSize = Integer.parseInt(args[++i]);
      } else if ("-fileSize".equals(args[i])) {
        fileSize = Integer.parseInt(args[++i]);
      } else {
        System.err.println("Illegal argument: " + args[i]);
        return;
      }
    }

    assert path_string != null;

    ExecutorService pool = Executors.newFixedThreadPool(threads);

    Path path = new Path(path_string);
    FileSystem fs = path.getFileSystem(new Configuration());
    List<Runnable> tasks = new ArrayList<>(threads);
    for (int i = 0; i < threads; i++) {
      Path dataPath = new Path(path, "data_" + i);
      Runnable task = null;
      switch (type) {
        case READ:
          task = new ReadTask(fs, dataPath, bufferSize, fileSize);
          break;
        case WRITE:
          if (fs.exists(path)) {
            fs.delete(path, true);
          }
          task = new WriteTask(fs, dataPath, bufferSize, fileSize);
          break;
      }
      tasks.add(task);
    }

    for (Runnable t : tasks) {
      pool.submit(t);
    }

    pool.shutdown();
    pool.awaitTermination(1, TimeUnit.DAYS);
    assert fs != null;
    fs.close();
  }

  static class WriteTask implements Runnable {
    FileSystem fs;
    int bufferSize;
    Path path;
    long writeSize;

    public WriteTask(FileSystem fs, Path output, int bufferSize, long fileSize) throws IOException {
      this.fs = fs;
      this.path = output;
      this.bufferSize = bufferSize;
      this.writeSize = fileSize << 20;
    }

    @Override
    public void run() {
      LOG.info("writing data to file: " + path.makeQualified(fs));
      try (FSDataOutputStream outputStream = fs.create(path, true, bufferSize);) {
        long remaining;
        byte[] buffer = new byte[bufferSize];
        for (remaining = writeSize; remaining > 0; remaining -= bufferSize) {
          int curSize = (bufferSize < remaining) ? bufferSize : (int) remaining;
          random.nextBytes(buffer);
          outputStream.write(buffer, 0, curSize);
        }
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
    }
  }

  static class ReadTask implements Runnable {
    FileSystem fs;
    int bufferSize;
    Path path;
    long readSize;

    public ReadTask(FileSystem fs, Path path, int bufferSize, long fileSize) throws IOException {
      this.fs = fs;
      this.path = path;
      this.bufferSize = bufferSize;
      this.readSize = fileSize << 20;
    }

    @Override
    public void run() {
      LOG.info("reading data from file: " + path.makeQualified(fs));
      try (FSDataInputStream inputStream = fs.open(path, bufferSize)) {
        byte[] buffer = new byte[bufferSize];
        long actualSize = 0L;
        while (actualSize < readSize) {
          int read = inputStream.read(buffer, 0, bufferSize);
          if (read < 0) {
            break;
          }
          actualSize += read;
        }
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
    }
  }
}

