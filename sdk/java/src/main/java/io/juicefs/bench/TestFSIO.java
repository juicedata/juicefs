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
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.io.compress.CompressionCodec;
import org.apache.hadoop.util.GenericOptionsParser;
import org.apache.hadoop.util.ReflectionUtils;
import org.apache.hadoop.util.StringUtils;

import java.io.Closeable;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.text.DecimalFormat;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;


/**
 * i/o benchmark.
 */
public class TestFSIO {
  private static final Log LOG = LogFactory.getLog(TestFSIO.class);
  private static final long MEGA = TestDFSIO.ByteMultiple.MB.value();
  private Configuration config;
  private static String baseDir = "/benchmarks/FSIO";
  private static final String USAGE =
          "Usage: " + TestFSIO.class.getSimpleName() +
                  " [genericOptions]" +
                  " -read [-random | -backward | -skip [-skipSize Size]] |" +
                  " -write | -append | -truncate | -clean" +
                  " [-compression codecClassName]" +
                  " [-nrFiles N]" +
                  " [-size Size[B|KB|MB|GB|TB]]" +
                  " [-resFile resultFileName] [-bufferSize Bytes]" +
                  " [-baseDir]";

  private static enum TestType {
    TEST_TYPE_READ("read"),
    TEST_TYPE_WRITE("write"),
    TEST_TYPE_CLEANUP("cleanup"),
    TEST_TYPE_APPEND("append"),
    TEST_TYPE_READ_RANDOM("random read"),
    TEST_TYPE_READ_BACKWARD("backward read"),
    TEST_TYPE_READ_SKIP("skip read"),
    TEST_TYPE_TRUNCATE("truncate");

    private String type;

    private TestType(String t) {
      type = t;
    }

    @Override // String
    public String toString() {
      return type;
    }
  }

  public TestFSIO(Configuration configuration) {
    this.config = configuration;
  }

  static abstract class TaskBase implements Callable<Long> {
    protected byte[] buffer;
    protected int bufferSize;
    protected FileSystem fs;
    protected int id;
    protected CompressionCodec compressionCodec;
    protected long totalSize;
    protected ThreadLocalRandom random = ThreadLocalRandom.current();
    protected Configuration config;


    public TaskBase(int id, Configuration config) {
      this.id = id;
      this.config = config;
      try {
        this.fs = new Path(getBaseDir(config)).getFileSystem(config);
      } catch (IOException e) {
        throw new RuntimeException("Cannot create file system.", e);
      }
      bufferSize = config.getInt("test.io.file.buffer.size", 4096);
      buffer = new byte[bufferSize];
      totalSize = config.getLong("test.nrbytes", MEGA);
      // grab compression
      String compression = config.get("test.io.compression.class", null);
      Class<? extends CompressionCodec> codec;

      // try to initialize codec
      try {
        codec = (compression == null) ? null :
                Class.forName(compression).asSubclass(CompressionCodec.class);
      } catch (Exception e) {
        throw new RuntimeException("Compression codec not found: ", e);
      }

      if (codec != null) {
        compressionCodec = ReflectionUtils.newInstance(codec, config);
      }
    }

    public byte[] fillBuffer() {
      random.nextBytes(buffer);
      return buffer;
    }

    /**
     * Perform io operation, usually read or write.
     */
    abstract Long doIO(int id, Closeable stream) throws IOException;

    /**
     * Create an input or output stream based on the specified file.
     * Subclasses should override this method to provide an actual stream.
     */
    public Closeable getIOStream(int id) throws IOException {
      return null;
    }

    /**
     * Collect stat data to be combined by a subsequent reducer.
     */
    void collectStats(int id,
                      long execTime,
                      Long size) throws IOException {
      float ioRateMbSec = (float) size * 1000 / (execTime * MEGA);
      LOG.info("task " + id + " Number of bytes processed = " + size);
      LOG.info("task " + id + " Exec time = " + execTime);
      LOG.info("task " + id + " IO rate = " + ioRateMbSec);

      Collector.INSTANCE.collectTasks(1);
      Collector.INSTANCE.collectSize(size);
      Collector.INSTANCE.collectExecTime(execTime);
      Collector.INSTANCE.collectRate(ioRateMbSec * 1000);
      Collector.INSTANCE.collectSQRate(ioRateMbSec * ioRateMbSec * 1000);
    }

    @Override
    public Long call() throws Exception {
      long execTime;
      long size;
      try (Closeable stream = getIOStream(id)) {
        long tStart = System.currentTimeMillis();

        size = doIO(id, stream);
        long tEnd = System.currentTimeMillis();
        execTime = tEnd - tStart;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
      collectStats(id, execTime, size);
      return size;
    }
  }

  static class Collector {
    static final Collector INSTANCE = new Collector();

    AtomicLong execTime = new AtomicLong(0L);
    AtomicLong size = new AtomicLong(0L);
    AtomicInteger tasks = new AtomicInteger(0);
    List<Double> rates = Collections.synchronizedList(new ArrayList<>());
    List<Double> sqrates = Collections.synchronizedList(new ArrayList<>());

    private Collector() {
    }

    void collectExecTime(long time) {
      this.execTime.getAndAdd(time);
    }

    void collectSize(long size) {
      this.size.getAndAdd(size);
    }

    void collectTasks(int task) {
      this.tasks.getAndAdd(task);
    }

    void collectRate(double rate) {
      this.rates.add(rate);
    }

    void collectSQRate(double sqrate) {
      this.sqrates.add(sqrate);
    }

    public long getExecTime() {
      return execTime.get();
    }

    public long getSize() {
      return size.get();
    }

    public int getTasks() {
      return tasks.get();
    }

    public double getRate() {
      return rates.stream().reduce(Double::sum).orElse(0.0);
    }

    public double getSqrate() {
      return sqrates.stream().reduce(Double::sum).orElse(0.0);
    }
  }

  static class ReadTask extends TaskBase {

    public ReadTask(Integer id, Configuration config) {
      super(id, config);
    }

    @Override
    public Closeable getIOStream(int id) throws IOException {
      // open file
      InputStream in = fs.open(new Path(getDataDir(config), "data_" + id));
      if (compressionCodec != null) {
        in = compressionCodec.createInputStream(in);
      }
      LOG.info("in = " + in.getClass().getName());
      return in;
    }

    @Override
    Long doIO(int id, Closeable stream) throws IOException {
      InputStream in = (InputStream) stream;
      long actualSize = 0;
      LOG.info("totalsize = " + totalSize);
      while (actualSize < totalSize) {
        int curSize = in.read(buffer, 0, bufferSize);
        if (curSize < 0) {
          break;
        }
        actualSize += curSize;
      }
      LOG.info("actualSize = " + actualSize);
      return actualSize;
    }
  }

  long readTest(FileSystem fs) throws IOException {
    long tStart = System.currentTimeMillis();
    try {
      runTest(ReadTask.class);
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return System.currentTimeMillis() - tStart;
  }

  static class RandomReadTask extends TaskBase {
    private Random rnd;
    private long fileSize;
    private long skipSize;

    public RandomReadTask(Integer id, Configuration config) {
      super(id, config);
      rnd = new Random();
      skipSize = config.getLong("test.io.skip.size", 0);
    }

    @Override
    public Closeable getIOStream(int id) throws IOException {
      Path filePath = new Path(getDataDir(config), "data_" + id);
      this.fileSize = fs.getFileStatus(filePath).getLen();
      InputStream in = fs.open(filePath);
      if (compressionCodec != null)
        in = new FSDataInputStream(compressionCodec.createInputStream(in));
      LOG.info("in = " + in.getClass().getName());
      LOG.info("skipSize = " + skipSize);
      return in;
    }

    @Override
    Long doIO(int id, Closeable stream) throws IOException {
      PositionedReadable in = (PositionedReadable) stream;
      long actualSize = 0;
      for (long pos = nextOffset(-1);
           actualSize < totalSize; pos = nextOffset(pos)) {
        int curSize = in.read(pos, buffer, 0, bufferSize);
        if (curSize < 0) break;
        actualSize += curSize;
      }
      return actualSize;
    }

    private long nextOffset(long current) {
      if (skipSize == 0)
        return rnd.nextInt((int) (fileSize));
      if (skipSize > 0)
        return (current < 0) ? 0 : (current + bufferSize + skipSize);
      // skipSize < 0
      return (current < 0) ? Math.max(0, fileSize - bufferSize) :
              Math.max(0, current + skipSize);
    }
  }

  private long randomReadTest(FileSystem fs) throws IOException {
    Path readDir = getRandomReadDir(config);
    fs.delete(readDir, true);
    long tStart = System.currentTimeMillis();
    try {
      runTest(RandomReadTask.class);
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return System.currentTimeMillis() - tStart;
  }

  static class TruncateTask extends TaskBase {
    private static final long DELAY = 100L;

    private Path filePath;
    private long fileSize;

    public TruncateTask(Integer id, Configuration config) {
      super(id, config);

    }

    @Override
    public Closeable getIOStream(int id) throws IOException {
      filePath = new Path(getDataDir(config), "data_" + id);
      fileSize = fs.getFileStatus(filePath).getLen();
      return null;
    }

    @Override
    Long doIO(int id, Closeable stream) throws IOException {
      boolean isClosed = fs.truncate(filePath, totalSize);
      for (int i = 0; !isClosed; i++) {
        try {
          Thread.sleep(DELAY);
        } catch (InterruptedException ignored) {
        }
        FileStatus status = fs.getFileStatus(filePath);
        assert status != null : "status is null";
        isClosed = (status.getLen() == totalSize);
      }
      return fileSize - totalSize;
    }
  }

  private long truncateTest(FileSystem fs) throws IOException {
    Path TruncateDir = getTruncateDir(config);
    fs.delete(TruncateDir, true);
    long tStart = System.currentTimeMillis();
    try {
      runTest(TruncateTask.class);
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return System.currentTimeMillis() - tStart;
  }

  static class WriteTask extends TaskBase {

    public WriteTask(Integer id, Configuration config) {
      super(id, config);
    }

    @Override
    public Closeable getIOStream(int id) throws IOException {
      // create file
      OutputStream out =
              fs.create(new Path(getDataDir(config), "data_" + id), true, bufferSize);
      if (compressionCodec != null) {
        out = compressionCodec.createOutputStream(out);
      }
      LOG.info("task " + id + " out = " + out.getClass().getName());
      return out;
    }

    @Override
    Long doIO(int id, Closeable stream) throws IOException {
      OutputStream out = (OutputStream) stream;

      // write to the file
      long nrRemaining;
      for (nrRemaining = totalSize; nrRemaining > 0; nrRemaining -= bufferSize) {
        int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
        out.write(fillBuffer(), 0, curSize);
      }
      return totalSize;
    }
  }

  long writeTest(FileSystem fs) throws IOException {
    Path writeDir = getWriteDir(config);
    fs.delete(getDataDir(config), true);
    fs.delete(writeDir, true);
    long tStart = System.currentTimeMillis();
    try {
      runTest(WriteTask.class);
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return System.currentTimeMillis() - tStart;
  }

  static class AppendTask extends TaskBase {

    public AppendTask(Integer id, Configuration config) {
      super(id, config);
    }

    @Override // IOMapperBase
    public Closeable getIOStream(int id) throws IOException {
      // open file for append
      OutputStream out =
              fs.append(new Path(getDataDir(config), "data_" + id), bufferSize);
      if (compressionCodec != null)
        out = compressionCodec.createOutputStream(out);
      LOG.info("out = " + out.getClass().getName());
      return out;
    }

    @Override
    Long doIO(int id, Closeable stream) throws IOException {
      OutputStream out = (OutputStream) stream;
      // write to the file
      long nrRemaining;
      for (nrRemaining = totalSize; nrRemaining > 0; nrRemaining -= bufferSize) {
        int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
        out.write(fillBuffer(), 0, curSize);
      }
      return totalSize;
    }
  }

  private long appendTest(FileSystem fs) throws IOException {
    Path appendDir = getAppendDir(config);
    fs.delete(appendDir, true);
    long tStart = System.currentTimeMillis();
    try {
      runTest(AppendTask.class);
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return System.currentTimeMillis() - tStart;
  }

  void runTest(Class<? extends Callable<Long>> clazz) throws Exception {
    int threads = config.getInt("test.threadsPerMap", 1);
    ExecutorService pool = Executors.newFixedThreadPool(threads);
    List<Future<Long>> futures = new ArrayList<>(threads);
    for (int i = 0; i < threads; i++) {
      Callable<Long> t = clazz.getDeclaredConstructor(Integer.class, Configuration.class).newInstance(i, config);
      futures.add(pool.submit(t));
    }
    for (Future<Long> future : futures) {
      future.get();
    }
  }

  public static void main(String[] args) {
    int res = -1;
    try {
      GenericOptionsParser parser = new GenericOptionsParser(args);
      Configuration configuration = parser.getConfiguration();
      TestFSIO bench = new TestFSIO(configuration);

      String[] toolArgs = parser.getRemainingArgs();
      res = bench.run(toolArgs);
    } catch (Exception e) {
      System.err.print(StringUtils.stringifyException(e));
      res = -2;
    }
    if (res == -1)
      System.err.print(USAGE);
    System.exit(res);
  }

  public int run(String[] args) throws IOException {
    TestType testType = null;
    int bufferSize = 1000000;
    long nrBytes = 1 * MEGA;
    int nrFiles = 1;
    long skipSize = 0;
    int threadsPerMap = 1;
    String compressionClass = null;
    String version = TestFSIO.class.getSimpleName() + ".1.8";

    LOG.info(version);
    if (args.length == 0) {
      System.err.println("Missing arguments.");
      return -1;
    }

    for (int i = 0; i < args.length; i++) {       // parse command line
      if (args[i].startsWith("-read")) {
        testType = TestType.TEST_TYPE_READ;
      } else if (args[i].equals("-write")) {
        testType = TestType.TEST_TYPE_WRITE;
      } else if (args[i].equals("-append")) {
        testType = TestType.TEST_TYPE_APPEND;
      } else if (args[i].equals("-random")) {
        if (testType != TestType.TEST_TYPE_READ) return -1;
        testType = TestType.TEST_TYPE_READ_RANDOM;
      } else if (args[i].equals("-backward")) {
        if (testType != TestType.TEST_TYPE_READ) return -1;
        testType = TestType.TEST_TYPE_READ_BACKWARD;
      } else if (args[i].equals("-skip")) {
        if (testType != TestType.TEST_TYPE_READ) return -1;
        testType = TestType.TEST_TYPE_READ_SKIP;
      } else if (args[i].equalsIgnoreCase("-truncate")) {
        testType = TestType.TEST_TYPE_TRUNCATE;
      } else if (args[i].equals("-clean")) {
        testType = TestType.TEST_TYPE_CLEANUP;
      } else if (args[i].startsWith("-compression")) {
        compressionClass = args[++i];
      } else if (args[i].equals("-nrFiles")) {
        nrFiles = Integer.parseInt(args[++i]);
      } else if (args[i].equals("-fileSize") || args[i].equals("-size")) {
        nrBytes = TestDFSIO.parseSize(args[++i]);
      } else if (args[i].equals("-skipSize")) {
        skipSize = TestDFSIO.parseSize(args[++i]);
      } else if (args[i].equals("-bufferSize")) {
        bufferSize = Integer.parseInt(args[++i]);
      } else if (args[i].equals("-baseDir")) {
        baseDir = args[++i];
      } else if (args[i].equals("-threadsPerMap")) {
        threadsPerMap = Integer.parseInt(args[++i]);
      } else {
        System.err.println("Illegal argument: " + args[i]);
        return -1;
      }
    }
    if (testType == null)
      return -1;
    if (testType == TestType.TEST_TYPE_READ_BACKWARD)
      skipSize = -bufferSize;
    else if (testType == TestType.TEST_TYPE_READ_SKIP && skipSize == 0)
      skipSize = bufferSize;

    LOG.info("nrFiles = " + nrFiles);
    LOG.info("nrBytes (MB) = " + TestDFSIO.toMB(nrBytes));
    LOG.info("bufferSize = " + bufferSize);
    if (skipSize > 0)
      LOG.info("skipSize = " + skipSize);
    LOG.info("baseDir = " + getBaseDir(config));
    LOG.info("threadsPerMap = " + threadsPerMap);

    if (compressionClass != null) {
      config.set("test.io.compression.class", compressionClass);
      LOG.info("compressionClass = " + compressionClass);
    }

    config.setInt("test.io.file.buffer.size", bufferSize);
    config.setLong("test.io.skip.size", skipSize);
    config.setBoolean("dfs.support.append", true);
    config.setInt("test.threadsPerMap", threadsPerMap);
    config.setLong("test.nrbytes", nrBytes);
    FileSystem fs = new Path(getBaseDir(config)).getFileSystem(config);

    if (testType == TestType.TEST_TYPE_CLEANUP) {
      cleanup(fs);
      return 0;
    }
    createControlFile(fs, nrBytes, nrFiles);
    long tStart = System.currentTimeMillis();
    switch (testType) {
      case TEST_TYPE_WRITE:
        writeTest(fs);
        break;
      case TEST_TYPE_READ:
        readTest(fs);
        break;
      case TEST_TYPE_APPEND:
        appendTest(fs);
        break;
      case TEST_TYPE_READ_RANDOM:
      case TEST_TYPE_READ_BACKWARD:
      case TEST_TYPE_READ_SKIP:
        randomReadTest(fs);
        break;
      case TEST_TYPE_TRUNCATE:
        truncateTest(fs);
        break;
      default:
    }
    long execTime = System.currentTimeMillis() - tStart;

    analyzeResult(fs, testType, execTime, null);
    return 0;
  }

  private static String getBaseDir(Configuration conf) {
    return baseDir;
  }

  private static Path getWriteDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_write");
  }

  private static Path getAppendDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_append");
  }

  private static Path getRandomReadDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_random_read");
  }

  private static Path getTruncateDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_truncate");
  }

  private static Path getDataDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_data");
  }

  void createControlFile(FileSystem fs, long nrBytes, int nrFiles) throws IOException {
  }

  void analyzeResult(FileSystem fs,
                     TestType testType,
                     long execTime,
                     String resFileName
  ) throws IOException {
    long tasks = Collector.INSTANCE.getTasks();
    long size = Collector.INSTANCE.getSize();
    long time = Collector.INSTANCE.getExecTime();
    double rate = Collector.INSTANCE.getRate();
    double sqrate = Collector.INSTANCE.getSqrate();
    double med = rate / 1000 / tasks;
    double stdDev = Math.sqrt(Math.abs(sqrate / 1000 / tasks - med * med));
    DecimalFormat df = new DecimalFormat("#.##");
    String resultLines[] = {
            "----- TestClient ----- : " + testType,
            "            Date & time: " + new Date(System.currentTimeMillis()),
            "        Number of files: " + tasks,
            " Total MBytes processed: " + df.format(TestDFSIO.toMB(size)),
            "      Throughput mb/sec: " + df.format(TestDFSIO.toMB(size) / TestDFSIO.msToSecs(time)),
            "Total Throughput mb/sec: " + df.format(med * tasks),
            " Average IO rate mb/sec: " + df.format(med),
            "  IO rate std deviation: " + df.format(stdDev),
            "     Test exec time sec: " + df.format(TestDFSIO.msToSecs(execTime)),
            ""};

    for (String resultLine : resultLines) {
      LOG.info(resultLine);
    }
  }

  private void cleanup(FileSystem fs)
          throws IOException {
    LOG.info("Cleaning up test files");
    fs.delete(new Path(getBaseDir(config)), true);
  }
}
