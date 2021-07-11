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
import org.apache.hadoop.conf.Configured;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FSDataOutputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.io.LongWritable;
import org.apache.hadoop.io.SequenceFile;
import org.apache.hadoop.io.SequenceFile.CompressionType;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.mapred.*;

import java.io.BufferedReader;
import java.io.DataInputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.text.SimpleDateFormat;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

@Parameters(commandDescription = "Distributed create/open/rename/delete meta benchmark")
public class NNBench extends Main.Command {
  private static final Log LOG = LogFactory.getLog(
          NNBench.class);

  protected static String CONTROL_DIR_NAME = "control";
  protected static String OUTPUT_DIR_NAME = "output";
  protected static String DATA_DIR_NAME = "data";

  public static long startTime =
          System.currentTimeMillis() + (30 * 1000); // default is 'now' + 30s

  @Parameter(description = "[create | open | rename | delete]", required = true)
  public static String operation;
  @Parameter(names = {"-maps"}, description = "number of maps")
  public long numberOfMaps = 1l; // default is 1
  @Parameter(names = {"-files"}, description = "number of files per thread")
  public long numberOfFiles = 1l; // default is 1
  @Parameter(names = {"-threads"}, description = "threads per map")
  public int threadsPerMap = 1;
  public long numberOfReduces = 1l; // default is 1
  @Parameter(names = {"-baseDir"}, description = "full path of dir on FileSystem", required = true)
  public String baseDir = "/benchmarks/NNBench";  // default
  @Parameter(names = {"-deleteBeforeRename"}, description = "delete files before or after rename operation")
  public static boolean deleteBeforeRename;
  @Parameter(names = {"-local"}, description = "run in local single process")
  private boolean local;

  // Supported operations
  private static final String OP_CREATE = "create";
  private static final String OP_OPEN = "open";
  private static final String OP_RENAME = "rename";
  private static final String OP_DELETE = "delete";

  // To display in the format that matches the NN and DN log format
  // Example: 2007-10-26 00:01:19,853
  static SimpleDateFormat sdf =
          new SimpleDateFormat("yyyy-MM-dd' 'HH:mm:ss','S");

  private static Configuration config = new Configuration();

  /**
   * Clean up the files before a test run
   *
   * @throws IOException on error
   */
  private void cleanupBeforeTestrun() throws IOException {
    FileSystem tempFS = new Path(baseDir).getFileSystem(config);

    // Delete the data directory only if it is the create/write operation
    if (operation.equals(OP_CREATE)) {
      LOG.info("Deleting data directory");
      tempFS.delete(new Path(baseDir, DATA_DIR_NAME), true);
    }
    tempFS.delete(new Path(baseDir, CONTROL_DIR_NAME), true);
    tempFS.delete(new Path(baseDir, OUTPUT_DIR_NAME), true);
  }

  /**
   * Create control files before a test run.
   * Number of files created is equal to the number of maps specified
   *
   * @throws IOException on error
   */
  private void createControlFiles() throws IOException {
    FileSystem tempFS = new Path(baseDir).getFileSystem(config);
    LOG.info("Creating " + numberOfMaps + " control files");

    for (int i = 0; i < numberOfMaps; i++) {
      String strFileName = "NNBench_Controlfile_" + i;
      Path filePath = new Path(new Path(baseDir, CONTROL_DIR_NAME),
              strFileName);

      SequenceFile.Writer writer = null;
      try {
        writer = SequenceFile.createWriter(tempFS, config, filePath, Text.class,
                LongWritable.class, CompressionType.NONE);
        writer.append(new Text(strFileName), new LongWritable(i));
      } finally {
        if (writer != null) {
          writer.close();
        }
      }
    }
  }

  /**
   * Analyze the results
   *
   * @throws IOException on error
   */
  private void analyzeResults() throws IOException {
    final FileSystem fs = new Path(baseDir).getFileSystem(config);
    Path reduceFile = new Path(new Path(baseDir, OUTPUT_DIR_NAME),
            "part-00000");

    DataInputStream in;
    in = new DataInputStream(fs.open(reduceFile));

    BufferedReader lines;
    lines = new BufferedReader(new InputStreamReader(in));

    long totalTime = 0l;
    long lateMaps = 0l;
    long numOfExceptions = 0l;
    long successfulFileOps = 0l;

    long mapStartTimeTPmS = 0l;
    long mapEndTimeTPmS = 0l;

    String resultTPSLine1 = null;
    String resultALLine1 = null;

    String line;
    while ((line = lines.readLine()) != null) {
      StringTokenizer tokens = new StringTokenizer(line, " \t\n\r\f%;");
      String attr = tokens.nextToken();
      if (attr.endsWith(":totalTime")) {
        totalTime = Long.parseLong(tokens.nextToken());
      } else if (attr.endsWith(":latemaps")) {
        lateMaps = Long.parseLong(tokens.nextToken());
      } else if (attr.endsWith(":numOfExceptions")) {
        numOfExceptions = Long.parseLong(tokens.nextToken());
      } else if (attr.endsWith(":successfulFileOps")) {
        successfulFileOps = Long.parseLong(tokens.nextToken());
      } else if (attr.endsWith(":mapStartTimeTPmS")) {
        mapStartTimeTPmS = Long.parseLong(tokens.nextToken());
      } else if (attr.endsWith(":mapEndTimeTPmS")) {
        mapEndTimeTPmS = Long.parseLong(tokens.nextToken());
      }
    }

    // Average latency is the average time to perform 'n' number of
    // operations, n being the number of files
    double avgLatency = (double) totalTime / successfulFileOps;

    double totalTimeTPS =
            (double) (1000 * successfulFileOps) / (mapEndTimeTPmS - mapStartTimeTPmS);

    if (operation.equals(OP_CREATE)) {
      resultTPSLine1 = "                           TPS: Create: " +
              (int) (totalTimeTPS);
      resultALLine1 = "                  Avg Lat (ms): Create: " + avgLatency;
    } else if (operation.equals(OP_OPEN)) {
      resultTPSLine1 = "                             TPS: Open: " +
              (int) totalTimeTPS;
      resultALLine1 = "                     Avg Lat (ms): Open: " + avgLatency;
    } else if (operation.equals(OP_RENAME)) {
      resultTPSLine1 = "                           TPS: Rename: " +
              (int) totalTimeTPS;
      resultALLine1 = "                   Avg Lat (ms): Rename: " + avgLatency;
    } else if (operation.equals(OP_DELETE)) {
      resultTPSLine1 = "                           TPS: Delete: " +
              (int) totalTimeTPS;
      resultALLine1 = "                   Avg Lat (ms): Delete: " + avgLatency;
    }

    String resultLines[] = {
            "-------------- NNBench -------------- : ",
            "                           Date & time: " + sdf.format(new Date(
                    System.currentTimeMillis())),
            "",
            "                        Test Operation: " + operation,
            "                            Start time: " +
                    sdf.format(new Date(startTime)),
            "                           Maps to run: " + numberOfMaps,
            "                       Threads per map: " + threadsPerMap,
            "                      Files per thread: " + numberOfFiles,
            "            Successful file operations: " + successfulFileOps,
            "",
            "        # maps that missed the barrier: " + lateMaps,
            "                          # exceptions: " + numOfExceptions,
            "",
            resultTPSLine1,
            resultALLine1,
            "",
            "              RAW DATA: TPS Total (ms): " + totalTime,
            "           RAW DATA: Job Duration (ms): " + (mapEndTimeTPmS - mapStartTimeTPmS),
            "                   RAW DATA: Late maps: " + lateMaps,
            "             RAW DATA: # of exceptions: " + numOfExceptions,
            ""};

    // Write to a file and also dump to log
    for (int i = 0; i < resultLines.length; i++) {
      LOG.info(resultLines[i]);
    }
  }

  /**
   * Run the test
   *
   * @throws IOException on error
   */
  public void runTests() throws IOException {

    JobConf job = new JobConf(config, NNBench.class);

    job.setJobName("NNBench-" + operation);
    FileInputFormat.setInputPaths(job, new Path(baseDir, CONTROL_DIR_NAME));
    job.setInputFormat(SequenceFileInputFormat.class);

    // Explicitly set number of max map attempts to 1.
    job.setMaxMapAttempts(1);

    // Explicitly turn off speculative execution
    job.setSpeculativeExecution(false);

    job.setMapperClass(NNBenchMapper.class);
    job.setReducerClass(NNBenchReducer.class);

    FileOutputFormat.setOutputPath(job, new Path(baseDir, OUTPUT_DIR_NAME));
    job.setOutputKeyClass(Text.class);
    job.setOutputValueClass(Text.class);
    job.setNumReduceTasks((int) numberOfReduces);
    JobClient.runJob(job);
  }

  /**
   * Validate the inputs
   */
  public void validateInputs() {
    // If it is not one of the four operations, then fail
    if (!operation.equals(OP_CREATE) &&
            !operation.equals(OP_OPEN) &&
            !operation.equals(OP_RENAME) &&
            !operation.equals(OP_DELETE)) {
      System.err.println("Error: Unknown operation: " + operation);
      System.exit(-1);
    }

    // If number of maps is a negative number, then fail
    // Hadoop allows the number of maps to be 0
    if (numberOfMaps < 0) {
      System.err.println("Error: Number of maps must be a positive number");
      System.exit(-1);
    }

    // If number of reduces is a negative number or 0, then fail
    if (numberOfReduces <= 0) {
      System.err.println("Error: Number of reduces must be a positive number");
      System.exit(-1);
    }

    // If number of files is a negative number, then fail
    if (numberOfFiles < 0) {
      System.err.println("Error: Number of files must be a positive number");
      System.exit(-1);
    }
  }

  @Override
  public void init() throws IOException {
    LOG.info("Test Inputs: ");
    LOG.info("           Test Operation: " + operation);
    LOG.info("               Start time: " + sdf.format(new Date(startTime)));
    if (!local) {
      LOG.info("           Number of maps: " + numberOfMaps);
    }
    LOG.info("Number of threads per map: " + threadsPerMap);
    LOG.info("          Number of files: " + numberOfFiles);
    LOG.info("                 Base dir: " + baseDir);

    // Set user-defined parameters, so the map method can access the values
    config.set("test.nnbench.operation", operation);
    config.setLong("test.nnbench.maps", numberOfMaps);
    config.setLong("test.nnbench.reduces", numberOfReduces);
    config.setLong("test.nnbench.starttime", startTime);
    config.setLong("test.nnbench.numberoffiles", numberOfFiles);
    config.set("test.nnbench.basedir", baseDir);
    config.setInt("test.nnbench.threadsPerMap", threadsPerMap);
    config.setBoolean("test.nnbench.deleteBeforeRename", deleteBeforeRename);
    config.setBoolean("test.nnbench.local", local);

    config.set("test.nnbench.datadir.name", DATA_DIR_NAME);
    config.set("test.nnbench.outputdir.name", OUTPUT_DIR_NAME);
    config.set("test.nnbench.controldir.name", CONTROL_DIR_NAME);
  }

  @Override
  public void run() throws IOException {
    validateInputs();
    cleanupBeforeTestrun();
    if (local) {
      localRun();
      return;
    }
    createControlFiles();
    runTests();
    analyzeResults();
  }

  private void localRun() {
    NNBenchMapper mapper = new NNBenchMapper();
    mapper.configure(new JobConf(config));

    ExecutorService pool = Executors.newFixedThreadPool(threadsPerMap, r -> {
      Thread t = new Thread(r);
      t.setDaemon(true);
      return t;
    });

    long start = System.currentTimeMillis();
    for (int i = 0; i < threadsPerMap; i++) {
      int threadNum = i;
      pool.submit(() -> {
        try {
          mapper.doMap(Collections.synchronizedList(new ArrayList<>()), 0, threadNum);
        } catch (IOException e) {
          e.printStackTrace();
          System.exit(1);
          throw new RuntimeException(e);
        }
      });
    }
    pool.shutdown();
    try {
      pool.awaitTermination(1, TimeUnit.DAYS);
    } catch (InterruptedException ignored) {
    }
    long end = System.currentTimeMillis();
    double totalTimeTPS =
            (double) (1000 * threadsPerMap * numberOfFiles) / (end - start);
    String[] resultLines = {
            "-------------- NNBench -------------- : ",
            "                           Date & time: " + sdf.format(new Date(
                    System.currentTimeMillis())),
            "",
            "                        Test Operation: " + operation,
            "                            Start time: " +
                    sdf.format(new Date(startTime)),
            "                               Threads: " + threadsPerMap,
            "                      Files per thread: " + numberOfFiles,
            "            Successful file operations: " + threadsPerMap * numberOfFiles,
            "",
            "                           TPS: Create: " + (int) (totalTimeTPS),
            "                  Avg Lat (ms): Create: " + String.format("%.2f", (double) (end - start) / (threadsPerMap * numberOfFiles)),
            "",
            "           RAW DATA: Job Duration (ms): " + (end - start),
            ""};

    for (int i = 0; i < resultLines.length; i++) {
      LOG.info(resultLines[i]);
    }
  }

  @Override
  public String getCommand() {
    return "nnbench";
  }

  @Override
  public void close() throws IOException {

  }

  /**
   * Mapper class
   */
  static class NNBenchMapper extends Configured
          implements Mapper<Text, LongWritable, Text, Text> {
    FileSystem filesystem = null;

    long numberOfFiles = 1l;
    boolean beforeRename = false;
    String baseDir = null;
    String dataDirName = null;
    String op = null;
    final int MAX_OPERATION_EXCEPTIONS = 1000;
    int threadsPerMap = 1;
    boolean local;

    ExecutorService executorService;

    // Data to collect from the operation

    /**
     * Constructor
     */
    public NNBenchMapper() {
    }


    /**
     * Mapper base implementation
     */
    public void configure(JobConf conf) {
      setConf(conf);
      local = conf.getBoolean("test.nnbench.local", false);
      try {
        baseDir = conf.get("test.nnbench.basedir");
        filesystem = new Path(baseDir).getFileSystem(conf);
      } catch (Exception e) {
        throw new RuntimeException("Cannot get file system.", e);
      }

      numberOfFiles = conf.getLong("test.nnbench.numberoffiles", 1l);
      dataDirName = conf.get("test.nnbench.datadir.name");
      op = conf.get("test.nnbench.operation");
      beforeRename = conf.getBoolean("test.nnbench.deleteBeforeRename", false);

      threadsPerMap = conf.getInt("test.nnbench.threadsPerMap", 1);
      executorService = Executors.newFixedThreadPool(threadsPerMap, r -> {
        Thread t = new Thread(r);
        t.setDaemon(true);
        return t;
      });
    }

    /**
     * Mapper base implementation
     */
    public void close() throws IOException {
    }

    /**
     * Returns when the current number of seconds from the epoch equals
     * the command line argument given by <code>-startTime</code>.
     * This allows multiple instances of this program, running on clock
     * synchronized nodes, to start at roughly the same time.
     *
     * @return true if the method was able to sleep for <code>-startTime</code>
     * without interruption; false otherwise
     */
    private boolean barrier() {
      if (local) {
        return true;
      }
      long startTime = getConf().getLong("test.nnbench.starttime", 0l);
      long currentTime = System.currentTimeMillis();
      long sleepTime = startTime - currentTime;
      boolean retVal = false;

      // If the sleep time is greater than 0, then sleep and return
      if (sleepTime > 0) {
        LOG.info("Waiting in barrier for: " + sleepTime + " ms");

        try {
          Thread.sleep(sleepTime);
          retVal = true;
        } catch (Exception e) {
          retVal = false;
        }
      }

      return retVal;
    }

    /**
     * Map method
     */
    public void map(Text key,
                    LongWritable value,
                    OutputCollector<Text, Text> output,
                    Reporter reporter) throws IOException {


      List<Entry> res = Collections.synchronizedList(new ArrayList<>());

      for (int i = 0; i < threadsPerMap; i++) {
        int threadNum = i;
        executorService.submit(() -> {
          try {
            doMap(res, value.get(), threadNum);
          } catch (IOException e) {
            throw new RuntimeException(e);
          }
        });
      }

      executorService.shutdown();
      try {
        executorService.awaitTermination(1, TimeUnit.DAYS);
      } catch (InterruptedException e) {
        throw new RuntimeException(e);
      }

      long successOps = 0L;
      for (Entry entry : res) {
        if (entry.key.toString().contains("successfulFileOps")) {
          successOps += Long.parseLong(entry.value.toString());
        }
        output.collect(entry.key, entry.value);
      }
      reporter.setStatus("Finish " + successOps + " files");
    }

    static class Entry {
      Text key;
      Text value;

      Entry(Text key, Text value) {
        this.key = key;
        this.value = value;
      }
    }

    private void doMap(List<Entry> res, long mapId, int threadNum) throws IOException {
      long startTimeTPmS = 0l;
      long endTimeTPms = 0l;

      AtomicLong successfulFileOps = new AtomicLong(0L);
      AtomicInteger numOfExceptions = new AtomicInteger(0);
      AtomicLong totalTime = new AtomicLong(0L);

      if (barrier()) {
        startTimeTPmS = System.currentTimeMillis();
        if (op.equals(OP_CREATE)) {
          doCreate(mapId, successfulFileOps, numOfExceptions, totalTime, threadNum);
        } else if (op.equals(OP_OPEN)) {
          doOpen(mapId, successfulFileOps, numOfExceptions, totalTime, threadNum);
        } else if (op.equals(OP_RENAME)) {
          doRenameOp(mapId, successfulFileOps, numOfExceptions, totalTime, threadNum);
        } else if (op.equals(OP_DELETE)) {
          doDeleteOp(mapId, successfulFileOps, numOfExceptions, totalTime, threadNum);
        }

        endTimeTPms = System.currentTimeMillis();
      } else {
        res.add(new Entry(new Text("l:latemaps"), new Text("1")));
      }

      // collect after the map end time is measured
      res.add(new Entry(new Text("l:totalTime"),
              new Text(String.valueOf(totalTime.get()))));
      res.add(new Entry(new Text("l:numOfExceptions"),
              new Text(String.valueOf(numOfExceptions.get()))));
      res.add(new Entry(new Text("l:successfulFileOps"),
              new Text(String.valueOf(successfulFileOps.get()))));
      res.add(new Entry(new Text("min:mapStartTimeTPmS"),
              new Text(String.valueOf(startTimeTPmS))));
      res.add(new Entry(new Text("max:mapEndTimeTPmS"),
              new Text(String.valueOf(endTimeTPms))));
    }

    /**
     * Create operation.
     */
    private void doCreate(long mapId,
                          AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) throws IOException {
      FSDataOutputStream out;

      for (long l = 0L; l < numberOfFiles; l++) {
        Path filePath = new Path(new Path(baseDir, dataDirName),
                new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_" + l)));
        boolean successfulOp = false;
        while (!successfulOp && numOfExceptions.get() < MAX_OPERATION_EXCEPTIONS) {
          try {
            // Set up timer for measuring AL (transaction #1)
            long startTime = System.currentTimeMillis();
            // Create the file
            out = filesystem.create(filePath, false);
            out.close();
            totalTime.addAndGet(System.currentTimeMillis() - startTime);
            successfulFileOps.getAndIncrement();
            successfulOp = true;
          } catch (IOException e) {
            LOG.info("Exception recorded in op: " +
                    "Create", e);
            numOfExceptions.getAndIncrement();
            throw e;
          }
        }
      }
    }

    /**
     * Open operation
     */
    private void doOpen(long mapId,
                        AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) throws IOException {
      FSDataInputStream input;

      for (long l = 0L; l < numberOfFiles; l++) {
        Path filePath = new Path(new Path(baseDir, dataDirName),
                new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_" + l)));

        boolean successfulOp = false;
        while (!successfulOp && numOfExceptions.get() < MAX_OPERATION_EXCEPTIONS) {
          try {
            // Set up timer for measuring AL
            long startTime = System.currentTimeMillis();
            input = filesystem.open(filePath);
            input.close();
            totalTime.addAndGet(System.currentTimeMillis() - startTime);
            successfulFileOps.getAndIncrement();
            successfulOp = true;
          } catch (IOException e) {
            LOG.info("Exception recorded in op: OpenRead " + e);
            numOfExceptions.getAndIncrement();
            throw e;
          }
        }
      }
    }

    /**
     * Rename operation
     */
    private void doRenameOp(long mapId,
                            AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) throws IOException {
      for (long l = 0L; l < numberOfFiles; l++) {
        Path filePath = new Path(new Path(baseDir, dataDirName),
                new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_" + l)));
        Path filePathR = new Path(new Path(baseDir, dataDirName),
                new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_r_" + l)));

        boolean successfulOp = false;
        while (!successfulOp && numOfExceptions.get() < MAX_OPERATION_EXCEPTIONS) {
          try {
            // Set up timer for measuring AL
            long startTime = System.currentTimeMillis();
            filesystem.rename(filePath, filePathR);
            totalTime.addAndGet(System.currentTimeMillis() - startTime);
            successfulFileOps.getAndIncrement();
            successfulOp = true;
          } catch (IOException e) {
            LOG.info("Exception recorded in op: Rename");
            numOfExceptions.getAndIncrement();
            throw e;
          }
        }
      }
    }

    /**
     * Delete operation
     */
    private void doDeleteOp(long mapId,
                            AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) throws IOException {
      for (long l = 0L; l < numberOfFiles; l++) {
        Path filePath;
        if (beforeRename) {
          filePath = new Path(new Path(baseDir, dataDirName),
                  new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_" + l)));
        } else {
          filePath = new Path(new Path(baseDir, dataDirName),
                  new Path(String.valueOf(mapId), new Path(String.valueOf(threadNum), "file_r_" + l)));
        }

        boolean successfulOp = false;
        while (!successfulOp && numOfExceptions.get() < MAX_OPERATION_EXCEPTIONS) {
          try {
            // Set up timer for measuring AL
            long startTime = System.currentTimeMillis();
            filesystem.delete(filePath, false);
            totalTime.addAndGet(System.currentTimeMillis() - startTime);
            successfulFileOps.getAndIncrement();
            successfulOp = true;
          } catch (IOException e) {
            LOG.info("Exception in recorded op: Delete");
            numOfExceptions.getAndIncrement();
            throw e;
          }
        }
      }
    }
  }

  /**
   * Reducer class
   */
  static class NNBenchReducer extends MapReduceBase
          implements Reducer<Text, Text, Text, Text> {

    protected String hostName;

    public NNBenchReducer() {
      LOG.info("Starting NNBenchReducer !!!");
      try {
        hostName = java.net.InetAddress.getLocalHost().getHostName();
      } catch (Exception e) {
        hostName = "localhost";
      }
      LOG.info("Starting NNBenchReducer on " + hostName);
    }

    /**
     * Reduce method
     */
    public void reduce(Text key,
                       Iterator<Text> values,
                       OutputCollector<Text, Text> output,
                       Reporter reporter
    ) throws IOException {
      String field = key.toString();

      reporter.setStatus("starting " + field + " ::host = " + hostName);

      // sum long values
      if (field.startsWith("l:")) {
        long lSum = 0;
        while (values.hasNext()) {
          lSum += Long.parseLong(values.next().toString());
        }
        output.collect(key, new Text(String.valueOf(lSum)));
      }

      if (field.startsWith("min:")) {
        long minVal = -1;
        while (values.hasNext()) {
          long value = Long.parseLong(values.next().toString());

          if (minVal == -1) {
            minVal = value;
          } else {
            if (value != 0 && value < minVal) {
              minVal = value;
            }
          }
        }
        output.collect(key, new Text(String.valueOf(minVal)));
      }

      if (field.startsWith("max:")) {
        long maxVal = -1;
        while (values.hasNext()) {
          long value = Long.parseLong(values.next().toString());

          if (maxVal == -1) {
            maxVal = value;
          } else {
            if (value > maxVal) {
              maxVal = value;
            }
          }
        }
        output.collect(key, new Text(String.valueOf(maxVal)));
      }

      reporter.setStatus("finished " + field + " ::host = " + hostName);
    }
  }
}
