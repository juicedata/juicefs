/**
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 * <p>
 * http://www.apache.org/licenses/LICENSE-2.0
 * <p>
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package io.juicefs.bench;

import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.apache.hadoop.conf.Configured;
import org.apache.hadoop.io.LongWritable;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.mapred.JobConf;
import org.apache.hadoop.mapred.Mapper;
import org.apache.hadoop.mapred.OutputCollector;
import org.apache.hadoop.mapred.Reporter;

import java.io.Closeable;
import java.io.IOException;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.atomic.AtomicLong;

public abstract class IOMapperBase extends Configured
        implements Mapper<Text, LongWritable, Text, Text> {
  private static final Log LOG = LogFactory.getLog(IOMapperBase.class);

  protected String hostName;
  protected Closeable stream;
  protected int threadsPerMap;
  protected int filesPerThread;
  protected ExecutorService pool;

  public IOMapperBase() {
  }

  @Override
  public void configure(JobConf conf) {
    setConf(conf);

    try {
      hostName = InetAddress.getLocalHost().getHostName();
    } catch (Exception e) {
      hostName = "localhost";
    }
    threadsPerMap = conf.getInt("test.threadsPerMap", 1);
    filesPerThread = conf.getInt("test.filesPerThread", 1);
    pool = Executors.newFixedThreadPool(threadsPerMap, r -> {
      Thread t = new Thread(r);
      t.setDaemon(true);
      return t;
    });
  }

  @Override
  public void close() throws IOException {
    pool.shutdown();
  }

  abstract Long doIO(Reporter reporter,
                     String name,
                     long value,  Closeable stream) throws IOException;


  public Closeable getIOStream(String name) throws IOException {
    return null;
  }

  abstract void collectStats(OutputCollector<Text, Text> output,
                             String name,
                             long execTime,
                             Long doIOReturnValue) throws IOException;

  @Override
  public void map(Text key,
                  LongWritable value,
                  OutputCollector<Text, Text> output,
                  Reporter reporter) throws IOException {
    String name = key.toString();
    long longValue = value.get();

    reporter.setStatus("starting " + name + " ::host = " + hostName);
    AtomicLong execTime = new AtomicLong(0L);
    List<Future<Long>> futures = new ArrayList<>(threadsPerMap);
    for (int i = 0; i < threadsPerMap; i++) {
      int id = i;
      Future<Long> future = pool.submit(() -> {
        long res = 0;
        for (int j = 0; j < filesPerThread; j++) {
          String filePath = String.format("%s/thread-%s/file-%s", name, id, j);
          try (Closeable stream = getIOStream(filePath)) {
            long tStart = System.currentTimeMillis();
            res += doIO(reporter, name, longValue, stream);
            long tEnd = System.currentTimeMillis();
            execTime.addAndGet(tEnd - tStart);
          } catch (IOException e) {
            throw new RuntimeException(e);
          }
        }
        return res;
      });
      futures.add(future);
    }

    Long result = 0L;
    try {
      for (Future<Long> future : futures) {
        result += future.get();
      }
    } catch (InterruptedException | ExecutionException e) {
      throw new RuntimeException(e);
    }

    collectStats(output, name, execTime.get(), result);

    reporter.setStatus("finished " + name + " ::host = " + hostName);
  }
}
