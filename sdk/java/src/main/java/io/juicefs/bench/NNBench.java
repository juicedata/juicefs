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

import java.io.*;
import java.text.SimpleDateFormat;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

/**
 * This program executes a specified operation that applies load to
 * the NameNode.
 * <p>
 * When run simultaneously on multiple nodes, this program functions
 * as a stress-test and benchmark for namenode, especially when
 * the number of bytes written to each file is small.
 * <p>
 * Valid operations are:
 * create_write
 * open_read
 * rename
 * delete
 * <p>
 * NOTE: The open_read, rename and delete operations assume that the files
 * they operate on are already available. The create_write operation
 * must be run before running the other operations.
 */

public class NNBench {
  private static final Log LOG = LogFactory.getLog(
          NNBench.class);

  protected static String CONTROL_DIR_NAME = "control";
  protected static String OUTPUT_DIR_NAME = "output";
  protected static String DATA_DIR_NAME = "data";
  protected static final String DEFAULT_RES_FILE_NAME = "NNBench_results.log";
  protected static final String NNBENCH_VERSION = "Meta Benchmark 1.0";

  public static String operation = "none";
  public static long numberOfMaps = 1l; // default is 1
  public static long numberOfReduces = 1l; // default is 1
  public static long startTime =
          System.currentTimeMillis() + (20 * 1000); // default is 'now' + 1min
  public static long numberOfFiles = 1l; // default is 1
  public static String baseDir = "/benchmarks/NNBench";  // default
  public static int threadsPerMap = 1;
  public static boolean deleteBeforeRename = false;

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
  private static void cleanupBeforeTestrun() throws IOException {
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
  private static void createControlFiles() throws IOException {
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
   * Display version
   */
  private static void displayVersion() {
    System.out.println(NNBENCH_VERSION);
  }

  /**
   * Display usage
   */
  private static void displayUsage() {
    String usage =
            "Usage: nnbench <options>\n" +
                    "Options:\n" +
                    "\t-operation <Available operations are " + OP_CREATE + " " +
                    OP_OPEN + " " + OP_RENAME + " " + OP_DELETE + ". " +
                    "This option is mandatory>\n" +
                    "\t * NOTE: The open, rename and delete operations assume " +
                    "that the files they operate on, are already available. " +
                    "The create operation must be run before running the " +
                    "other operations.\n" +
                    "\t-maps <number of maps. default is 1. This is not mandatory>\n" +
                    "\t-reduces <number of reduces. default is 1. This is not mandatory>\n" +
                    "\t-startTime <time to start, given in seconds from the epoch. " +
                    "Make sure this is far enough into the future, so all maps " +
                    "(operations) will start at the same time>. " +
                    "default is launch time + 2 mins. This is not mandatory \n" +
                    "\t-numberOfFiles <number of files to create. default is 1. " +
                    "This is not mandatory>\n" +
                    "\t-baseDir <base FS path. default is /becnhmarks/NNBench. " +
                    "This is not mandatory>\n" +
                    "\t-deleteBeforeRename\n" +
                    "\t-help: Display the help statement\n";


    System.out.println(usage);
  }

  /**
   * check for arguments and fail if the values are not specified
   *
   * @param index  positional number of an argument in the list of command
   *               line's arguments
   * @param length total number of arguments
   */
  public static void checkArgs(final int index, final int length) {
    if (index == length) {
      displayUsage();
      System.exit(-1);
    }
  }

  /**
   * Parse input arguments
   *
   * @param args array of command line's parameters to be parsed
   */
  public static void parseInputs(final String[] args) {
    // If there are no command line arguments, exit
    if (args.length == 0) {
      displayUsage();
      System.exit(-1);
    }

    // Parse command line args
    for (int i = 0; i < args.length; i++) {
      if (args[i].equals("-operation")) {
        operation = args[++i];
      } else if (args[i].equals("-maps")) {
        checkArgs(i + 1, args.length);
        numberOfMaps = Long.parseLong(args[++i]);
      } else if (args[i].equals("-reduces")) {
        checkArgs(i + 1, args.length);
        numberOfReduces = Long.parseLong(args[++i]);
      } else if (args[i].equals("-startTime")) {
        checkArgs(i + 1, args.length);
        startTime = Long.parseLong(args[++i]) * 1000;
      } else if (args[i].equals("-numberOfFiles")) {
        checkArgs(i + 1, args.length);
        numberOfFiles = Long.parseLong(args[++i]);
      } else if (args[i].equals("-baseDir")) {
        checkArgs(i + 1, args.length);
        baseDir = args[++i];
      } else if (args[i].equals("-threadsPerMap")) {
        checkArgs(i + 1, args.length);
        threadsPerMap = Integer.parseInt(args[++i]);
      } else if (args[i].equals("-deleteBeforeRename")) {
        deleteBeforeRename = true;
        ++i;
      } else if (args[i].equals("-help")) {
        displayUsage();
        System.exit(-1);
      }
    }

    LOG.info("Test Inputs: ");
    LOG.info("           Test Operation: " + operation);
    LOG.info("               Start time: " + sdf.format(new Date(startTime)));
    LOG.info("           Number of maps: " + numberOfMaps);
    LOG.info("        Number of reduces: " + numberOfReduces);
    LOG.info("          Number of files: " + numberOfFiles);
    LOG.info("                 Base dir: " + baseDir);
    LOG.info("          Threads per map: " + threadsPerMap);

    // Set user-defined parameters, so the map method can access the values
    config.set("test.nnbench.operation", operation);
    config.setLong("test.nnbench.maps", numberOfMaps);
    config.setLong("test.nnbench.reduces", numberOfReduces);
    config.setLong("test.nnbench.starttime", startTime);
    config.setLong("test.nnbench.numberoffiles", numberOfFiles);
    config.set("test.nnbench.basedir", baseDir);
    config.setInt("test.nnbench.threadsPerMap", threadsPerMap);
    config.setBoolean("test.nnbench.deleteBeforeRename", deleteBeforeRename);

    config.set("test.nnbench.datadir.name", DATA_DIR_NAME);
    config.set("test.nnbench.outputdir.name", OUTPUT_DIR_NAME);
    config.set("test.nnbench.controldir.name", CONTROL_DIR_NAME);
  }

  /**
   * Analyze the results
   *
   * @throws IOException on error
   */
  private static void analyzeResults() throws IOException {
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
      resultALLine1 = "                   Avg Lat (ms): Create: " + avgLatency;
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
            "                               Version: " + NNBENCH_VERSION,
            "                           Date & time: " + sdf.format(new Date(
                    System.currentTimeMillis())),
            "",
            "                        Test Operation: " + operation,
            "                            Start time: " +
                    sdf.format(new Date(startTime)),
            "                           Maps to run: " + numberOfMaps,
            "                       Threads per map: " + threadsPerMap,
            "                        Reduces to run: " + numberOfReduces,
            "                       Number of files: " + numberOfFiles,
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

    PrintStream res = new PrintStream(new FileOutputStream(
            DEFAULT_RES_FILE_NAME, true));

    // Write to a file and also dump to log
    for (int i = 0; i < resultLines.length; i++) {
      LOG.info(resultLines[i]);
      res.println(resultLines[i]);
    }
  }

  /**
   * Run the test
   *
   * @throws IOException on error
   */
  public static void runTests() throws IOException {

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
  public static void validateInputs() {
    // If it is not one of the four operations, then fail
    if (!operation.equals(OP_CREATE) &&
            !operation.equals(OP_OPEN) &&
            !operation.equals(OP_RENAME) &&
            !operation.equals(OP_DELETE)) {
      System.err.println("Error: Unknown operation: " + operation);
      displayUsage();
      System.exit(-1);
    }

    // If number of maps is a negative number, then fail
    // Hadoop allows the number of maps to be 0
    if (numberOfMaps < 0) {
      System.err.println("Error: Number of maps must be a positive number");
      displayUsage();
      System.exit(-1);
    }

    // If number of reduces is a negative number or 0, then fail
    if (numberOfReduces <= 0) {
      System.err.println("Error: Number of reduces must be a positive number");
      displayUsage();
      System.exit(-1);
    }

    // If number of files is a negative number, then fail
    if (numberOfFiles < 0) {
      System.err.println("Error: Number of files must be a positive number");
      displayUsage();
      System.exit(-1);
    }

  }

  /**
   * Main method for running the NNBench benchmarks
   *
   * @param args array of command line arguments
   * @throws IOException indicates a problem with test startup
   */
  public static void main(String[] args) throws IOException {
    // Display the application version string
    displayVersion();

    // Parse the inputs
    parseInputs(args);

    // Validate inputs
    validateInputs();

    // Clean up files before the test run
    cleanupBeforeTestrun();

    // Create control files before test run
    createControlFiles();

    // Run the tests as a map reduce job
    runTests();

    // Analyze results
    analyzeResults();
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
        executorService.awaitTermination(1000, TimeUnit.HOURS);
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
                          AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) {
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
          }
        }
      }
    }

    /**
     * Open operation
     */
    private void doOpen(long mapId,
                        AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) {
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
          }
        }
      }
    }

    /**
     * Rename operation
     */
    private void doRenameOp(long mapId,
                            AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) {
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
          }
        }
      }
    }

    /**
     * Delete operation
     */
    private void doDeleteOp(long mapId,
                            AtomicLong successfulFileOps, AtomicInteger numOfExceptions, AtomicLong totalTime, int threadNum) {
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
