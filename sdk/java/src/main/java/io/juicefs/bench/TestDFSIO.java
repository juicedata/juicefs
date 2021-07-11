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

import com.beust.jcommander.Parameter;
import com.beust.jcommander.Parameters;
import io.juicefs.Main;
import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.io.LongWritable;
import org.apache.hadoop.io.SequenceFile;
import org.apache.hadoop.io.SequenceFile.CompressionType;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.io.compress.CompressionCodec;
import org.apache.hadoop.mapred.*;
import org.apache.hadoop.util.ReflectionUtils;

import java.io.*;
import java.text.DecimalFormat;
import java.util.Date;
import java.util.Locale;
import java.util.StringTokenizer;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;


@Parameters(commandDescription = "Distributed i/o benchmark")
public class TestDFSIO extends Main.Command {
  // Constants
  private static final Log LOG = LogFactory.getLog(TestDFSIO.class);
  private static final String BASE_FILE_NAME = "test_io_";
  private static final long MEGA = ByteMultiple.MB.value();

  @Parameter(description = "[-read | -write]", required = true)
  private String testType;
  @Parameter(names = {"-random"}, description = "random read")
  private boolean random;
  @Parameter(names = {"-backward"}, description = "backward read")
  private boolean backward;
  @Parameter(names = {"-skip"}, description = "skip read")
  private boolean skip;
  @Parameter(names = {"-local"}, description = "run in local single process")
  private boolean local;

  @Parameter(names = {"-baseDir"}, description = "full path of dir on FileSystem", required = true)
  private String baseDir = "/benchmarks/DFSIO";

  @Parameter(names = {"-bufferSize"}, description = "bufferSize[B|KB|MB|GB|TB]")
  private String bufferSize = "1MB";
  @Parameter(names = {"-size"}, description = "per file size[B|KB|MB|GB|TB]")
  private String size = "1GB";
  @Parameter(names = {"-maps"}, description = "number of maps")
  private int maps = 1;
  @Parameter(names = {"-threads"}, description = "threads per map")
  private int threadsPerMap = 1;
  @Parameter(names = {"-files"}, description = "number of files per thread")
  private int filesPerThread = 1;
  @Parameter(names = {"-skipSize"}, description = "skipSize[B|KB|MB|GB|TB]")
  private String skipSize;
  @Parameter(names = {"-compression"}, description = "codecClassName")
  String compression = null;
  @Parameter(names = {"-randomBytes"}, description = "generate randomBytes")
  boolean randomBytes = false;

  private FileSystem fs;
  private TestType type;
  private Configuration config;

  @Override
  public void close() throws IOException {
    this.fs.close();
  }

  private enum TestType {
    TEST_TYPE_READ("read"),
    TEST_TYPE_WRITE("write"),
    TEST_TYPE_CLEANUP("cleanup"),
    TEST_TYPE_APPEND("append"),
    TEST_TYPE_READ_RANDOM("random read"),
    TEST_TYPE_READ_BACKWARD("backward read"),
    TEST_TYPE_READ_SKIP("skip read"),
    TEST_TYPE_TRUNCATE("truncate");

    private String type;

    TestType(String t) {
      type = t;
    }

    @Override // String
    public String toString() {
      return type;
    }
  }

  static enum ByteMultiple {
    B(1L),
    KB(0x400L),
    MB(0x100000L),
    GB(0x40000000L),
    TB(0x10000000000L);

    private long multiplier;

    private ByteMultiple(long mult) {
      multiplier = mult;
    }

    long value() {
      return multiplier;
    }

    static ByteMultiple parseString(String sMultiple) {
      if (sMultiple == null || sMultiple.isEmpty()) // MB by default
      {
        return MB;
      }
      String sMU = sMultiple.toUpperCase(Locale.ENGLISH);
      if (B.name().toUpperCase(Locale.ENGLISH).endsWith(sMU)) {
        return B;
      }
      if (KB.name().toUpperCase(Locale.ENGLISH).endsWith(sMU)) {
        return KB;
      }
      if (MB.name().toUpperCase(Locale.ENGLISH).endsWith(sMU)) {
        return MB;
      }
      if (GB.name().toUpperCase(Locale.ENGLISH).endsWith(sMU)) {
        return GB;
      }
      if (TB.name().toUpperCase(Locale.ENGLISH).endsWith(sMU)) {
        return TB;
      }
      throw new IllegalArgumentException("Unsupported ByteMultiple " + sMultiple);
    }
  }

  public TestDFSIO() {
    this.config = new Configuration();
  }

  @Override
  public void init() throws IOException {
    this.config = new Configuration();
    config.setBoolean("dfs.support.append", true);
    this.fs = new Path(baseDir).getFileSystem(config);

    checkArgs();
    switch (testType) {
      case "-read":
        type = TestType.TEST_TYPE_READ;
        break;
      case "-write":
        type = TestType.TEST_TYPE_WRITE;
        break;
      case "-append":
        type = TestType.TEST_TYPE_APPEND;
        break;
      case "-truncate":
        type = TestType.TEST_TYPE_TRUNCATE;
        break;
      case "-clean":
        type = TestType.TEST_TYPE_CLEANUP;
        break;
      default:
        throw new IllegalArgumentException("wrong type");
    }
    if (random) {
      type = TestType.TEST_TYPE_READ_RANDOM;
    } else if (backward) {
      type = TestType.TEST_TYPE_READ_BACKWARD;
    } else if (skip) {
      type = TestType.TEST_TYPE_READ_SKIP;
    }
    int bufferSizeBytes = (int) parseSize(bufferSize);
    long sizeInBytes = parseSize(size);
    long skipSizeInBytes = skipSize == null ? 0 : parseSize(skipSize);
    if (type == TestType.TEST_TYPE_READ_BACKWARD) {
      skipSizeInBytes = -bufferSizeBytes;
    } else if (type == TestType.TEST_TYPE_READ_SKIP && skipSizeInBytes == 0) {
      skipSizeInBytes = bufferSizeBytes;
    }

    config.setInt("test.io.file.buffer.size", bufferSizeBytes);
    config.setLong("test.io.skip.size", skipSizeInBytes);
    config.setBoolean("dfs.support.append", true);
    config.setInt("test.threadsPerMap", threadsPerMap);
    config.setInt("test.filesPerThread", filesPerThread);
    config.set("test.basedir", baseDir);
    config.setBoolean("test.randomBytes", randomBytes);

    LOG.info("type = " + type);
    if (!local) {
      LOG.info("maps = " + maps);
    }
    LOG.info("threads = " + threadsPerMap);
    LOG.info("files = " + filesPerThread);
    LOG.info("randomBytes = " + randomBytes);
    LOG.info("fileSize (MB) = " + TestDFSIO.toMB(sizeInBytes));
    LOG.info("bufferSize = " + bufferSize);
    if (skipSizeInBytes > 0)
      LOG.info("skipSize = " + skipSize);
    LOG.info("baseDir = " + baseDir);

    createControlFile(fs, sizeInBytes, maps);
    if (compression != null) {
      LOG.info("compressionClass = " + compression);
    }
  }

  private void checkArgs() {
    if (!testType.equals("-read")) {
      if (random || backward || skip) {
        throw new IllegalArgumentException("random, backward, skip are only valid under read");
      }
    } else {
      boolean[] conds = {random, backward, skip};
      int trueCount = 0;
      for (boolean cond : conds) {
        if (cond) {
          trueCount++;
          if (trueCount > 1) {
            throw new IllegalArgumentException("random, backward, skip are mutually exclusive");
          }
        }
      }
    }
  }

  private void localRun(TestType testType) throws IOException {
    IOStatMapper ioer;
    switch (testType) {
      case TEST_TYPE_READ:
        ioer = new ReadMapper();
        break;
      case TEST_TYPE_WRITE:
        ioer = new WriteMapper();
        fs.delete(getDataDir(config), true);
        break;
      case TEST_TYPE_APPEND:
        ioer = new AppendMapper();
        break;
      case TEST_TYPE_READ_RANDOM:
      case TEST_TYPE_READ_BACKWARD:
      case TEST_TYPE_READ_SKIP:
        ioer = new RandomReadMapper();
        break;
      case TEST_TYPE_TRUNCATE:
        ioer = new TruncateMapper();
        break;
      default:
        return;
    }
    ExecutorService pool = Executors.newFixedThreadPool(threadsPerMap, r -> {
      Thread t = new Thread(r);
      t.setDaemon(true);
      return t;
    });

    ioer.configure(new JobConf(config));
    AtomicLong sizeProcessed = new AtomicLong();
    long start = System.currentTimeMillis();
    for (int i = 0; i < threadsPerMap; i++) {
      int id = i;
      pool.execute(() -> {
        for (int j = 0; j < filesPerThread; j++) {
          String name = String.format("%s/thread-%s/file-%s", getFileName(0), id, j);
          try {
            Long res = ioer.doIO(Reporter.NULL, name, parseSize(size), ioer.getIOStream(name));
            sizeProcessed.addAndGet(res);
          } catch (IOException e) {
            e.printStackTrace();
            System.exit(1);
          }
        }
      });

    }
    pool.shutdown();
    try {
      pool.awaitTermination(1, TimeUnit.DAYS);
    } catch (InterruptedException ignored) {
    }
    long end = System.currentTimeMillis();

    DecimalFormat df = new DecimalFormat("#.##");
    String resultLines[] = {
            "----- TestClient ----- : " + testType,
            "            Date & time: " + new Date(System.currentTimeMillis()),
            "      Number of threads: " + threadsPerMap,
            "Number files per thread: " + filesPerThread,
            "            Total files: " + threadsPerMap * filesPerThread,
            " Total MBytes processed: " + df.format(TestDFSIO.toMB(sizeProcessed.get())),
            "Total Throughput mb/sec: " + df.format(TestDFSIO.toMB(sizeProcessed.get()) / TestDFSIO.msToSecs(end - start)),
            "     Test exec time sec: " + df.format(TestDFSIO.msToSecs(end - start)),
            ""};

    for (String resultLine : resultLines) {
      LOG.info(resultLine);
    }
  }

  @Override
  public void run() throws IOException {
    if (type == TestType.TEST_TYPE_CLEANUP) {
      cleanup(fs);
      return;
    }
    if (local) {
      localRun(type);
      return;
    }
    long tStart = System.currentTimeMillis();
    switch (type) {
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

    analyzeResult(fs, type, execTime);
  }

  @Override
  public String getCommand() {
    return "dfsio";
  }

  private String getBaseDir(Configuration conf) {
    return baseDir;
  }

  private Path getControlDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_control");
  }

  private Path getWriteDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_write");
  }

  private Path getReadDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_read");
  }

  private Path getAppendDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_append");
  }

  private Path getRandomReadDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_random_read");
  }

  private Path getTruncateDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_truncate");
  }

  private Path getDataDir(Configuration conf) {
    return new Path(getBaseDir(conf), "io_data");
  }


  @SuppressWarnings("deprecation")
  private void createControlFile(FileSystem fs,
                                 long nrBytes, // in bytes
                                 int maps
  ) throws IOException {
    LOG.info("creating control file: " + nrBytes + " bytes, " + maps + " files");
    final int maxDirItems = config.getInt("dfs.namenode.fs-limits.max-directory-items", 1024 * 1024);
    Path controlDir = getControlDir(config);

    if (maps > maxDirItems) {
      final String message = "The directory item limit of " + controlDir +
              " is exceeded: limit=" + maxDirItems + " items=" + maps;
      throw new IOException(message);
    }

    fs.delete(controlDir, true);

    for (int i = 0; i < maps; i++) {
      String name = getFileName(i);
      Path controlFile = new Path(controlDir, "in_file_" + name);
      SequenceFile.Writer writer = null;
      try {
        writer = SequenceFile.createWriter(fs, config, controlFile,
                Text.class, LongWritable.class,
                CompressionType.NONE);
        writer.append(new Text(name), new LongWritable(nrBytes));
      } catch (Exception e) {
        throw new IOException(e.getLocalizedMessage());
      } finally {
        if (writer != null) {
          writer.close();
        }
      }
    }
    LOG.info("created control files for: " + maps + " files");
  }

  private static String getFileName(int fIdx) {
    return BASE_FILE_NAME + fIdx;
  }

  /**
   * Write/Read mapper base class.
   * <p>
   * Collects the following statistics per task:
   * <ul>
   * <li>number of tasks completed</li>
   * <li>number of bytes written/read</li>
   * <li>execution time</li>
   * <li>i/o rate</li>
   * <li>i/o rate squared</li>
   * </ul>
   */
  private abstract static class IOStatMapper extends IOMapperBase {
    protected CompressionCodec compressionCodec;
    private static final ThreadLocalRandom random = ThreadLocalRandom.current();
    private boolean randomBytes;
    protected FileSystem fs;
    protected String baseDir;
    protected ThreadLocal<byte[]> buffer;
    protected int bufferSize;

    IOStatMapper() {
    }

    public byte[] getBuffer() {
      if (randomBytes) {
        random.nextBytes(buffer.get());
      }
      return buffer.get();
    }

    @Override // Mapper
    public void configure(JobConf conf) {
      super.configure(conf);
      bufferSize = conf.getInt("test.io.file.buffer.size", 4096);
      buffer = ThreadLocal.withInitial(() -> new byte[bufferSize]);
      try {
        baseDir = conf.get("test.basedir");
        fs = new Path(baseDir).getFileSystem(conf);
      } catch (IOException e) {
        throw new RuntimeException("Cannot create file system.", e);
      }
      randomBytes = conf.getBoolean("test.randomBytes", false);

      // grab compression
      String compression = getConf().get("test.io.compression.class", null);
      Class<? extends CompressionCodec> codec;

      // try to initialize codec
      try {
        codec = (compression == null) ? null :
                Class.forName(compression).asSubclass(CompressionCodec.class);
      } catch (Exception e) {
        throw new RuntimeException("Compression codec not found: ", e);
      }

      if (codec != null) {
        compressionCodec = (CompressionCodec)
                ReflectionUtils.newInstance(codec, getConf());
      }

    }

    Path getDataDir() {
      return new Path(baseDir, "io_data");
    }

    @Override
      // IOMapperBase
    void collectStats(OutputCollector<Text, Text> output,
                      String name,
                      long execTime,
                      Long objSize) throws IOException {
      long totalSize = objSize;
      float ioRateMbSec = (float) totalSize * 1000 / (execTime * MEGA);
      LOG.info("Number of bytes processed = " + totalSize);
      LOG.info("Exec time = " + execTime);
      LOG.info("IO rate = " + ioRateMbSec);

      output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "tasks"),
              new Text(String.valueOf(threadsPerMap * filesPerThread)));
      output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "size"),
              new Text(String.valueOf(totalSize)));
      output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "time"),
              new Text(String.valueOf(execTime)));
      output.collect(new Text(AccumulatingReducer.VALUE_TYPE_FLOAT + "rate"),
              new Text(String.valueOf(ioRateMbSec * 1000 * threadsPerMap)));
      output.collect(new Text(AccumulatingReducer.VALUE_TYPE_FLOAT + "sqrate"),
              new Text(String.valueOf(ioRateMbSec * ioRateMbSec * 1000 * threadsPerMap)));
    }
  }

  /**
   * Write mapper class.
   */
  public static class WriteMapper extends IOStatMapper {

    public WriteMapper() {
    }

    @Override // IOMapperBase
    public Closeable getIOStream(String name) throws IOException {
      // create file
      Path f = new Path(getDataDir(), name);
      fs.mkdirs(f.getParent());
      OutputStream out =
              fs.create(f, false, bufferSize);
      if (compressionCodec != null) {
        out = compressionCodec.createOutputStream(out);
      }
      LOG.info("out = " + out.getClass().getName());
      return out;
    }

    @Override // IOMapperBase
    public Long doIO(Reporter reporter,
                     String name,
                     long totalSize, // in bytes
                     Closeable stream) throws IOException {
      OutputStream out = (OutputStream) stream;

      // write to the file
      long nrRemaining;
      for (nrRemaining = totalSize; nrRemaining > 0; nrRemaining -= bufferSize) {
        int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
        out.write(getBuffer(), 0, curSize);
        reporter.setStatus("writing " + name + "@" +
                (totalSize - nrRemaining) + "/" + totalSize
                + " ::host = " + hostName);
      }
      return Long.valueOf(totalSize);
    }
  }

  private long writeTest(FileSystem fs) throws IOException {
    Path writeDir = getWriteDir(config);
    fs.delete(getDataDir(config), true);
    fs.delete(writeDir, true);
    long tStart = System.currentTimeMillis();
    runIOTest(WriteMapper.class, writeDir);
    long execTime = System.currentTimeMillis() - tStart;
    return execTime;
  }

  private void runIOTest(
          Class<? extends Mapper<Text, LongWritable, Text, Text>> mapperClass,
          Path outputDir) throws IOException {
    JobConf job = new JobConf(config, TestDFSIO.class);

    FileInputFormat.setInputPaths(job, getControlDir(config));
    job.setInputFormat(SequenceFileInputFormat.class);

    job.setMapperClass(mapperClass);
    job.setReducerClass(AccumulatingReducer.class);

    FileOutputFormat.setOutputPath(job, outputDir);
    job.setOutputKeyClass(Text.class);
    job.setOutputValueClass(Text.class);
    job.setNumReduceTasks(1);
    JobClient.runJob(job);
  }

  /**
   * Append mapper class.
   */
  public static class AppendMapper extends IOStatMapper {

    public AppendMapper() {
    }

    @Override // IOMapperBase
    public Closeable getIOStream(String name) throws IOException {
      // open file for append
      OutputStream out =
              fs.append(new Path(getDataDir(), name), bufferSize);
      if (compressionCodec != null)
        out = compressionCodec.createOutputStream(out);
      LOG.info("out = " + out.getClass().getName());
      return out;
    }

    @Override // IOMapperBase
    public Long doIO(Reporter reporter,
                     String name,
                     long totalSize, // in bytes
                     Closeable stream) throws IOException {
      OutputStream out = (OutputStream) stream;
      // write to the file
      long nrRemaining;
      for (nrRemaining = totalSize; nrRemaining > 0; nrRemaining -= bufferSize) {
        int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
        out.write(getBuffer(), 0, curSize);
        reporter.setStatus("writing " + name + "@" +
                (totalSize - nrRemaining) + "/" + totalSize
                + " ::host = " + hostName);
      }
      return totalSize;
    }


  }

  private long appendTest(FileSystem fs) throws IOException {
    Path appendDir = getAppendDir(config);
    fs.delete(appendDir, true);
    long tStart = System.currentTimeMillis();
    runIOTest(AppendMapper.class, appendDir);
    return System.currentTimeMillis() - tStart;
  }

  /**
   * Read mapper class.
   */
  public static class ReadMapper extends IOStatMapper {

    public ReadMapper() {
    }

    @Override // IOMapperBase
    public Closeable getIOStream(String name) throws IOException {
      // open file
      InputStream in = fs.open(new Path(getDataDir(), name));
      if (compressionCodec != null) {
        in = compressionCodec.createInputStream(in);
      }
      LOG.info("in = " + in.getClass().getName());
      return in;
    }

    @Override // IOMapperBase
    public Long doIO(Reporter reporter,
                     String name,
                     long totalSize, // in bytes
                     Closeable stream) throws IOException {
      InputStream in = (InputStream) stream;
      long actualSize = 0;
      while (actualSize < totalSize) {
        int curSize = in.read(buffer.get(), 0, bufferSize);
        if (curSize < 0) {
          break;
        }
        actualSize += curSize;
        reporter.setStatus("reading " + name + "@" +
                actualSize + "/" + totalSize
                + " ::host = " + hostName);
      }
      return actualSize;
    }
  }

  private long readTest(FileSystem fs) throws IOException {
    Path readDir = getReadDir(config);
    fs.delete(readDir, true);
    long tStart = System.currentTimeMillis();
    runIOTest(ReadMapper.class, readDir);
    return System.currentTimeMillis() - tStart;
  }

  /**
   * Mapper class for random reads.
   * The mapper chooses a position in the file and reads bufferSize
   * bytes starting at the chosen position.
   * It stops after reading the totalSize bytes, specified by -size.
   * <p>
   * There are three type of reads.
   * 1) Random read always chooses a random position to read from: skipSize = 0
   * 2) Backward read reads file in reverse order                : skipSize < 0
   * 3) Skip-read skips skipSize bytes after every read          : skipSize > 0
   */
  public static class RandomReadMapper extends IOStatMapper {
    private ThreadLocalRandom rnd;
    private long fileSize;
    private long skipSize;

    @Override // Mapper
    public void configure(JobConf conf) {
      super.configure(conf);
      skipSize = conf.getLong("test.io.skip.size", 0);
    }

    public RandomReadMapper() {
      rnd = ThreadLocalRandom.current();
    }

    @Override // IOMapperBase
    public Closeable getIOStream(String name) throws IOException {
      Path filePath = new Path(getDataDir(), name);
      this.fileSize = fs.getFileStatus(filePath).getLen();
      InputStream in = fs.open(filePath);
      if (compressionCodec != null)
        in = new FSDataInputStream(compressionCodec.createInputStream(in));
      LOG.info("in = " + in.getClass().getName());
      LOG.info("skipSize = " + skipSize);
      return in;
    }

    @Override // IOMapperBase
    public Long doIO(Reporter reporter,
                     String name,
                     long totalSize, // in bytes
                     Closeable stream) throws IOException {
      PositionedReadable in = (PositionedReadable) stream;
      long actualSize = 0;
      for (long pos = nextOffset(-1);
           actualSize < totalSize; pos = nextOffset(pos)) {
        int curSize = in.read(pos, buffer.get(), 0, bufferSize);
        if (curSize < 0) break;
        actualSize += curSize;
        reporter.setStatus("reading " + name + "@" +
                actualSize + "/" + totalSize
                + " ::host = " + hostName);
      }
      return actualSize;
    }

    /**
     * Get next offset for reading.
     * If current < 0 then choose initial offset according to the read type.
     *
     * @param current offset
     * @return
     */
    private long nextOffset(long current) {
      if (skipSize == 0)
        return rnd.nextLong(fileSize);
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
    runIOTest(RandomReadMapper.class, readDir);
    return System.currentTimeMillis() - tStart;
  }

  /**
   * Truncate mapper class.
   * The mapper truncates given file to the newLength, specified by -size.
   */
  public static class TruncateMapper extends IOStatMapper {
    private static final long DELAY = 100L;

    private Path filePath;
    private long fileSize;

    @Override // IOMapperBase
    public Closeable getIOStream(String name) throws IOException {
      filePath = new Path(getDataDir(), name);
      fileSize = fs.getFileStatus(filePath).getLen();
      return null;
    }

    @Override // IOMapperBase
    public Long doIO(Reporter reporter,
                     String name,
                     long newLength, // in bytes
                     Closeable stream) throws IOException {
      boolean isClosed = fs.truncate(filePath, newLength);
      reporter.setStatus("truncating " + name + " to newLength " +
              newLength + " ::host = " + hostName);
      for (int i = 0; !isClosed; i++) {
        try {
          Thread.sleep(DELAY);
        } catch (InterruptedException ignored) {
        }
        FileStatus status = fs.getFileStatus(filePath);
        assert status != null : "status is null";
        isClosed = (status.getLen() == newLength);
        reporter.setStatus("truncate recover for " + name + " to newLength " +
                newLength + " attempt " + i + " ::host = " + hostName);
      }
      return fileSize - newLength;
    }
  }

  private long truncateTest(FileSystem fs) throws IOException {
    Path TruncateDir = getTruncateDir(config);
    fs.delete(TruncateDir, true);
    long tStart = System.currentTimeMillis();
    runIOTest(TruncateMapper.class, TruncateDir);
    return System.currentTimeMillis() - tStart;
  }

  /**
   * Returns size in bytes.
   *
   * @param arg = {d}[B|KB|MB|GB|TB]
   * @return
   */
  static long parseSize(String arg) {
    String[] args = arg.split("\\D", 2);  // get digits
    assert args.length <= 2;
    long nrBytes = Long.parseLong(args[0]);
    String bytesMult = arg.substring(args[0].length()); // get byte multiple
    return nrBytes * ByteMultiple.parseString(bytesMult).value();
  }

  static float toMB(long bytes) {
    return ((float) bytes) / MEGA;
  }

  static float msToSecs(long timeMillis) {
    return timeMillis / 1000.0f;
  }

  private void analyzeResult(FileSystem fs,
                             TestType testType,
                             long execTime
  ) throws IOException {
    Path reduceFile = getReduceFilePath(testType);
    long tasks = 0;
    long size = 0;
    long time = 0;
    float rate = 0;
    float sqrate = 0;
    DataInputStream in = null;
    BufferedReader lines = null;
    try {
      in = new DataInputStream(fs.open(reduceFile));
      lines = new BufferedReader(new InputStreamReader(in));
      String line;
      while ((line = lines.readLine()) != null) {
        StringTokenizer tokens = new StringTokenizer(line, " \t\n\r\f%");
        String attr = tokens.nextToken();
        if (attr.endsWith(":tasks"))
          tasks = Long.parseLong(tokens.nextToken());
        else if (attr.endsWith(":size"))
          size = Long.parseLong(tokens.nextToken());
        else if (attr.endsWith(":time"))
          time = Long.parseLong(tokens.nextToken());
        else if (attr.endsWith(":rate"))
          rate = Float.parseFloat(tokens.nextToken());
        else if (attr.endsWith(":sqrate"))
          sqrate = Float.parseFloat(tokens.nextToken());
      }
    } finally {
      if (in != null) in.close();
      if (lines != null) lines.close();
    }

    double med = rate / 1000 / tasks;
    double stdDev = Math.sqrt(Math.abs(sqrate / 1000 / tasks - med * med));
    DecimalFormat df = new DecimalFormat("#.##");
    String resultLines[] = {
            "----- TestDFSIO ----- : " + testType,
            "            Date & time: " + new Date(System.currentTimeMillis()),
            "        Number of files: " + tasks,
            " Total MBytes processed: " + df.format(toMB(size)),
            "Total Throughput MB/sec: " + df.format(toMB(size) / msToSecs(time) * tasks),
            " Average IO rate MB/sec: " + df.format(med),
            "  IO rate std deviation: " + df.format(stdDev),
            "     Test exec time sec: " + df.format(msToSecs(execTime)),
            ""};
    for (String resultLine : resultLines) {
      LOG.info(resultLine);
    }
  }

  private Path getReduceFilePath(TestType testType) {
    switch (testType) {
      case TEST_TYPE_WRITE:
        return new Path(getWriteDir(config), "part-00000");
      case TEST_TYPE_APPEND:
        return new Path(getAppendDir(config), "part-00000");
      case TEST_TYPE_READ:
        return new Path(getReadDir(config), "part-00000");
      case TEST_TYPE_READ_RANDOM:
      case TEST_TYPE_READ_BACKWARD:
      case TEST_TYPE_READ_SKIP:
        return new Path(getRandomReadDir(config), "part-00000");
      case TEST_TYPE_TRUNCATE:
        return new Path(getTruncateDir(config), "part-00000");
      default:
    }
    return null;
  }

  private void cleanup(FileSystem fs)
          throws IOException {
    LOG.info("Cleaning up test files");
    fs.delete(new Path(getBaseDir(config)), true);
  }
}
