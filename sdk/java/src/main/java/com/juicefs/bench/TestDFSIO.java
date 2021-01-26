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
import org.apache.hadoop.util.StringUtils;
import org.apache.hadoop.util.Tool;
import org.apache.hadoop.util.ToolRunner;

import java.io.*;
import java.text.DecimalFormat;
import java.util.Date;
import java.util.Locale;
import java.util.Random;
import java.util.StringTokenizer;
import java.util.concurrent.ThreadLocalRandom;

/**
 * Distributed i/o benchmark.
 * <p>
 * This test writes into or reads from a specified number of files.
 * Number of bytes to write or read is specified as a parameter to the test.
 * Each file is accessed in a separate map task.
 * <p>
 * The reducer collects the following statistics:
 * <ul>
 * <li>number of tasks completed</li>
 * <li>number of bytes written/read</li>
 * <li>execution time</li>
 * <li>io rate</li>
 * <li>io rate squared</li>
 * </ul>
 * <p>
 * Finally, the following information is appended to a local file
 * <ul>
 * <li>read or write test</li>
 * <li>date and time the test finished</li>
 * <li>number of files</li>
 * <li>total number of bytes processed</li>
 * <li>throughput in mb/sec (total number of bytes / sum of processing times)</li>
 * <li>average i/o rate in mb/sec per file</li>
 * <li>standard deviation of i/o rate </li>
 * </ul>
 */
public class TestDFSIO implements Tool {
    // Constants
    private static final Log LOG = LogFactory.getLog(TestDFSIO.class);
    private static final int DEFAULT_BUFFER_SIZE = 1000000;
    private static final String BASE_FILE_NAME = "test_io_";
    private static final String DEFAULT_RES_FILE_NAME = "TestDFSIO_results.log";
    private static final long MEGA = ByteMultiple.MB.value();
    private static final int DEFAULT_NR_BYTES = 128;
    private static final int DEFAULT_NR_FILES = 4;
    private static String baseDir = "/benchmarks/DFSIO";
    private static final String USAGE =
            "Usage: " + TestDFSIO.class.getSimpleName() +
                    " [genericOptions]" +
                    " -read [-random | -backward | -skip [-skipSize Size]] |" +
                    " -write | -append | -truncate | -clean" +
                    " [-compression codecClassName]" +
                    " [-nrFiles N]" +
                    " [-size Size[B|KB|MB|GB|TB]]" +
                    " [-resFile resultFileName] [-bufferSize Bytes]" +
                    " [-baseDir]";

    private Configuration config;

    static {
        Configuration.addDefaultResource("hdfs-default.xml");
        Configuration.addDefaultResource("hdfs-site.xml");
        Configuration.addDefaultResource("mapred-default.xml");
        Configuration.addDefaultResource("mapred-site.xml");
    }

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

    private static String getBaseDir(Configuration conf) {
        return baseDir;
    }

    private static Path getControlDir(Configuration conf) {
        return new Path(getBaseDir(conf), "io_control");
    }

    private static Path getWriteDir(Configuration conf) {
        return new Path(getBaseDir(conf), "io_write");
    }

    private static Path getReadDir(Configuration conf) {
        return new Path(getBaseDir(conf), "io_read");
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


    @SuppressWarnings("deprecation")
    private void createControlFile(FileSystem fs,
                                   long nrBytes, // in bytes
                                   int nrFiles
    ) throws IOException {
        LOG.info("creating control file: " + nrBytes + " bytes, " + nrFiles + " files");
        final int maxDirItems = config.getInt("dfs.namenode.fs-limits.max-directory-items", 1024 * 1024);
        Path controlDir = getControlDir(config);

        if (nrFiles > maxDirItems) {
            final String message = "The directory item limit of " + controlDir +
                    " is exceeded: limit=" + maxDirItems + " items=" + nrFiles;
            throw new IOException(message);
        }

        fs.delete(controlDir, true);

        for (int i = 0; i < nrFiles; i++) {
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
                writer = null;
            }
        }
        LOG.info("created control files for: " + nrFiles + " files");
    }

    private static String getFileName(int fIdx) {
        return BASE_FILE_NAME + Integer.toString(fIdx);
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
    private abstract static class IOStatMapper extends IOMapperBase<Long> {
        protected CompressionCodec compressionCodec;
        private static final ThreadLocalRandom random = ThreadLocalRandom.current();

        IOStatMapper() {
        }

        public byte[] getBuffer() {
            random.nextBytes(buffer.get());
            return buffer.get();
        }

        @Override // Mapper
        public void configure(JobConf conf) {
            super.configure(conf);
            try {
                baseDir = conf.get("test.basedir");
                fs = new Path(getBaseDir(conf)).getFileSystem(conf);
            } catch (IOException e) {
                throw new RuntimeException("Cannot create file system.", e);
            }

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

        @Override
            // IOMapperBase
        void collectStats(OutputCollector<Text, Text> output,
                          String name,
                          long execTime,
                          Long objSize) throws IOException {
            long totalSize = objSize.longValue();
            float ioRateMbSec = (float) totalSize * 1000 / (execTime * MEGA);
            LOG.info("Number of bytes processed = " + totalSize);
            LOG.info("Exec time = " + execTime);
            LOG.info("IO rate = " + ioRateMbSec);

            output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "tasks"),
                    new Text(String.valueOf(threadPerMap)));
            output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "size"),
                    new Text(String.valueOf(totalSize)));
            output.collect(new Text(AccumulatingReducer.VALUE_TYPE_LONG + "time"),
                    new Text(String.valueOf(execTime)));
            output.collect(new Text(AccumulatingReducer.VALUE_TYPE_FLOAT + "rate"),
                    new Text(String.valueOf(ioRateMbSec * 1000 * threadPerMap)));
            output.collect(new Text(AccumulatingReducer.VALUE_TYPE_FLOAT + "sqrate"),
                    new Text(String.valueOf(ioRateMbSec * ioRateMbSec * 1000 * threadPerMap)));
        }
    }

    /**
     * Write mapper class.
     */
    public static class WriteMapper extends IOStatMapper {

        public WriteMapper() {
        }

        @Override // IOMapperBase
        public Closeable getIOStream(String name, int id) throws IOException {
            // create file
            OutputStream out =
                    fs.create(new Path(getDataDir(getConf()), name + "_" + id), true, bufferSize);
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
                         int id, Closeable stream) throws IOException {
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
        public Closeable getIOStream(String name, int id) throws IOException {
            // open file for append
            OutputStream out =
                    fs.append(new Path(getDataDir(getConf()), name + "_" + id), bufferSize);
            if (compressionCodec != null)
                out = compressionCodec.createOutputStream(out);
            LOG.info("out = " + out.getClass().getName());
            return out;
        }

        @Override // IOMapperBase
        public Long doIO(Reporter reporter,
                         String name,
                         long totalSize, // in bytes
                         int id, Closeable stream) throws IOException {
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

    private long appendTest(FileSystem fs) throws IOException {
        Path appendDir = getAppendDir(config);
        fs.delete(appendDir, true);
        long tStart = System.currentTimeMillis();
        runIOTest(AppendMapper.class, appendDir);
        long execTime = System.currentTimeMillis() - tStart;
        return execTime;
    }

    /**
     * Read mapper class.
     */
    public static class ReadMapper extends IOStatMapper {

        public ReadMapper() {
        }

        @Override // IOMapperBase
        public Closeable getIOStream(String name, int id) throws IOException {
            // open file
            InputStream in = fs.open(new Path(getDataDir(getConf()), name + "_" + id));
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
                         int id, Closeable stream) throws IOException {
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
            return Long.valueOf(actualSize);
        }
    }

    private long readTest(FileSystem fs) throws IOException {
        Path readDir = getReadDir(config);
        fs.delete(readDir, true);
        long tStart = System.currentTimeMillis();
        runIOTest(ReadMapper.class, readDir);
        long execTime = System.currentTimeMillis() - tStart;
        return execTime;
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
        private Random rnd;
        private long fileSize;
        private long skipSize;

        @Override // Mapper
        public void configure(JobConf conf) {
            super.configure(conf);
            skipSize = conf.getLong("test.io.skip.size", 0);
        }

        public RandomReadMapper() {
            rnd = new Random();
        }

        @Override // IOMapperBase
        public Closeable getIOStream(String name, int id) throws IOException {
            Path filePath = new Path(getDataDir(getConf()), name + "_" + id);
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
                         int id, Closeable stream) throws IOException {
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
            return Long.valueOf(actualSize);
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
        runIOTest(RandomReadMapper.class, readDir);
        long execTime = System.currentTimeMillis() - tStart;
        return execTime;
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
        public Closeable getIOStream(String name, int id) throws IOException {
            filePath = new Path(getDataDir(getConf()), name + "_" + id);
            fileSize = fs.getFileStatus(filePath).getLen();
            return null;
        }

        @Override // IOMapperBase
        public Long doIO(Reporter reporter,
                         String name,
                         long newLength, // in bytes
                         int id, Closeable stream) throws IOException {
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
            return Long.valueOf(fileSize - newLength);
        }
    }

    private long truncateTest(FileSystem fs) throws IOException {
        Path TruncateDir = getTruncateDir(config);
        fs.delete(TruncateDir, true);
        long tStart = System.currentTimeMillis();
        runIOTest(TruncateMapper.class, TruncateDir);
        long execTime = System.currentTimeMillis() - tStart;
        return execTime;
    }

    private void sequentialTest(FileSystem fs,
                                TestType testType,
                                long fileSize, // in bytes
                                int nrFiles
    ) throws IOException {
        IOStatMapper ioer = null;
        switch (testType) {
            case TEST_TYPE_READ:
                ioer = new ReadMapper();
                break;
            case TEST_TYPE_WRITE:
                ioer = new WriteMapper();
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
        for (int i = 0; i < nrFiles; i++)
            ioer.doIO(Reporter.NULL,
                    BASE_FILE_NAME + Integer.toString(i),
                    fileSize, 0, ioer.getIOStream(BASE_FILE_NAME + Integer.toString(i), 0));
    }

    public static void main(String[] args) {
        TestDFSIO bench = new TestDFSIO();
        int res = -1;
        try {
            res = ToolRunner.run(bench, args);
        } catch (Exception e) {
            System.err.print(StringUtils.stringifyException(e));
            res = -2;
        }
        if (res == -1)
            System.err.print(USAGE);
        System.exit(res);
    }

    @Override // Tool
    public int run(String[] args) throws IOException {
        TestType testType = null;
        int bufferSize = DEFAULT_BUFFER_SIZE;
        long nrBytes = 1 * MEGA;
        int nrFiles = 1;
        long skipSize = 0;
        String resFileName = DEFAULT_RES_FILE_NAME;
        int threadsPerMap = 1;
        String compressionClass = null;
        boolean isSequential = false;
        String version = TestDFSIO.class.getSimpleName() + ".1.8";

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
            } else if (args[i].startsWith("-seq")) {
                isSequential = true;
            } else if (args[i].startsWith("-compression")) {
                compressionClass = args[++i];
            } else if (args[i].equals("-nrFiles")) {
                nrFiles = Integer.parseInt(args[++i]);
            } else if (args[i].equals("-fileSize") || args[i].equals("-size")) {
                nrBytes = parseSize(args[++i]);
            } else if (args[i].equals("-skipSize")) {
                skipSize = parseSize(args[++i]);
            } else if (args[i].equals("-bufferSize")) {
                bufferSize = Integer.parseInt(args[++i]);
            } else if (args[i].equals("-resFile")) {
                resFileName = args[++i];
            } else if (args[i].equals("-baseDir")) {
                baseDir = args[++i];
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
        LOG.info("nrBytes (MB) = " + toMB(nrBytes));
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
        config.set("test.basedir", baseDir);
        FileSystem fs = new Path(getBaseDir(config)).getFileSystem(config);

        if (isSequential) {
            long tStart = System.currentTimeMillis();
            sequentialTest(fs, testType, nrBytes, nrFiles);
            long execTime = System.currentTimeMillis() - tStart;
            String resultLine = "Seq Test exec time sec: " + msToSecs(execTime);
            LOG.info(resultLine);
            return 0;
        }
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

        analyzeResult(fs, testType, execTime, resFileName);
        return 0;
    }

    @Override // Configurable
    public Configuration getConf() {
        return this.config;
    }

    @Override // Configurable
    public void setConf(Configuration conf) {
        this.config = conf;
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
                               long execTime,
                               String resFileName
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
                "      Throughput mb/sec: " + df.format(toMB(size) / msToSecs(time)),
                "Total Throughput mb/sec: " + df.format(toMB(size) / msToSecs(execTime)),
                " Average IO rate mb/sec: " + df.format(med),
                "  IO rate std deviation: " + df.format(stdDev),
                "     Test exec time sec: " + df.format(msToSecs(execTime)),
                ""};

        PrintStream res = null;
        try {
            res = new PrintStream(new FileOutputStream(new File(resFileName), true));
            for (int i = 0; i < resultLines.length; i++) {
                LOG.info(resultLines[i]);
                res.println(resultLines[i]);
            }
        } finally {
            if (res != null) res.close();
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

    private void analyzeResult(FileSystem fs, TestType testType, long execTime)
            throws IOException {
        String dir = System.getProperty("test.build.dir", "target/test-dir");
        analyzeResult(fs, testType, execTime, dir + "/" + DEFAULT_RES_FILE_NAME);
    }

    private void cleanup(FileSystem fs)
            throws IOException {
        LOG.info("Cleaning up test files");
        fs.delete(new Path(getBaseDir(config)), true);
    }
}
