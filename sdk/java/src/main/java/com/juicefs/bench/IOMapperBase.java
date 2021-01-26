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
package com.juicefs.bench;

import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.apache.hadoop.conf.Configured;
import org.apache.hadoop.fs.FileSystem;
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

/**
 * Base mapper class for IO operations.
 * <p>
 * Two abstract method {@link #doIO(Reporter, String, long, int, Closeable)} and
 * {@link #collectStats(OutputCollector, String, long, Object)} should be
 * overloaded in derived classes to define the IO operation and the
 * statistics data to be collected by subsequent reducers.
 */
public abstract class IOMapperBase<T> extends Configured
        implements Mapper<Text, LongWritable, Text, Text> {
    private static final Log LOG = LogFactory.getLog(IOMapperBase.class);

    protected ThreadLocal<byte[]> buffer;
    protected int bufferSize;
    protected FileSystem fs;
    protected String hostName;
    protected Closeable stream;
    protected int threadPerMap;
    protected ExecutorService pool;

    public IOMapperBase() {
    }

    @Override
    public void configure(JobConf conf) {
        setConf(conf);
        try {
            fs = FileSystem.get(conf);
        } catch (Exception e) {
            throw new RuntimeException("Cannot create file system.", e);
        }
        bufferSize = conf.getInt("test.io.file.buffer.size", 4096);
        buffer = ThreadLocal.withInitial(() -> new byte[bufferSize]);

        try {
            hostName = InetAddress.getLocalHost().getHostName();
        } catch (Exception e) {
            hostName = "localhost";
        }
        threadPerMap = conf.getInt("test.threadsPerMap", 1);
        pool = Executors.newFixedThreadPool(threadPerMap);
    }

    @Override
    public void close() throws IOException {
        pool.shutdown();
    }

    /**
     * Perform io operation, usually read or write.
     *
     * @param reporter
     * @param name     file name
     * @param value    offset within the file
     * @param id
     * @param stream
     * @return object that is passed as a parameter to
     * {@link #collectStats(OutputCollector, String, long, Object)}
     * @throws IOException
     */
    abstract T doIO(Reporter reporter,
                    String name,
                    long value, int id, Closeable stream) throws IOException;

    /**
     * Create an input or output stream based on the specified file.
     * Subclasses should override this method to provide an actual stream.
     *
     * @param name file name
     * @param id
     * @return the stream
     * @throws IOException
     */
    public Closeable getIOStream(String name, int id) throws IOException {
        return null;
    }

    /**
     * Collect stat data to be combined by a subsequent reducer.
     *
     * @param output
     * @param name            file name
     * @param execTime        IO execution time
     * @param doIOReturnValue value returned by {@link #doIO(Reporter, String, long, int, Closeable)}
     * @throws IOException
     */
    abstract void collectStats(OutputCollector<Text, Text> output,
                               String name,
                               long execTime,
                               T doIOReturnValue) throws IOException;

    /**
     * Map file name and offset into statistical data.
     * <p>
     * The map task is to get the
     * <tt>key</tt>, which contains the file name, and the
     * <tt>value</tt>, which is the offset within the file.
     * <p>
     * The parameters are passed to the abstract method
     * {@link #doIO(Reporter, String, long, int, Closeable)}, which performs the io operation,
     * usually read or write data, and then
     * {@link #collectStats(OutputCollector, String, long, Object)}
     * is called to prepare stat data for a subsequent reducer.
     */
    @Override
    public void map(Text key,
                    LongWritable value,
                    OutputCollector<Text, Text> output,
                    Reporter reporter) throws IOException {
        String name = key.toString();
        long longValue = value.get();

        reporter.setStatus("starting " + name + " ::host = " + hostName);
        AtomicLong execTime = new AtomicLong(0L);
        List<Future<Long>> futures = new ArrayList<>(threadPerMap);
        for (int i = 0; i < threadPerMap; i++) {
            int id = i;
            Future<Long> future = pool.submit(() -> {
                try (Closeable stream = getIOStream(name, id)) {
                    long tStart = System.currentTimeMillis();
                    T result = doIO(reporter, name, longValue, id, stream);
                    long tEnd = System.currentTimeMillis();
                    execTime.addAndGet(tEnd - tStart);
                    return (Long) result;
                } catch (IOException e) {
                    throw new RuntimeException(e);
                }
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

        collectStats(output, name, execTime.get(), (T) result);

        reporter.setStatus("finished " + name + " ::host = " + hostName);
    }
}
