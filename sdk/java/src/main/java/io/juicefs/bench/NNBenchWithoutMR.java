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

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FSDataOutputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.mapred.JobConf;
import org.apache.hadoop.util.StringUtils;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.Date;

/**
 * This program executes a specified operation that applies load to
 * the NameNode. Possible operations include create/writing files,
 * opening/reading files, renaming files, and deleting files.
 * <p>
 * When run simultaneously on multiple nodes, this program functions
 * as a stress-test and benchmark for namenode, especially when
 * the number of bytes written to each file is small.
 * <p>
 * This version does not use the map reduce framework
 */
public class NNBenchWithoutMR {

  private static final Logger LOG =
          LoggerFactory.getLogger(NNBenchWithoutMR.class);

  // variable initialzed from command line arguments
  private static int numFiles = 0;
  private static Path baseDir = null;
  private static boolean deleteBeforeRename = false;

  // variables initialized in main()
  private static FileSystem fileSys = null;
  private static Path taskDir = null;
  private static long maxExceptionsPerFile = 200;

  static private void handleException(String operation, Throwable e,
                                      int singleFileExceptions) {
    LOG.warn("Exception while " + operation + ": " +
            StringUtils.stringifyException(e));
    if (singleFileExceptions >= maxExceptionsPerFile) {
      throw new RuntimeException(singleFileExceptions +
              " exceptions for a single file exceeds threshold. Aborting");
    }
  }

  /**
   * Create a given number of files.
   *
   * @return the number of exceptions caught
   */
  static int create() {
    int totalExceptions = 0;
    FSDataOutputStream out;
    for (int index = 0; index < numFiles; index++) {
      int singleFileExceptions = 0;
      // create file until is succeeds or max exceptions reached
      try {
        out = fileSys.create(
                new Path(taskDir, "" + index), false);
        out.close();
      } catch (IOException ioe) {
        totalExceptions++;
        handleException("creating file #" + index, ioe,
                ++singleFileExceptions);
      }
    }
    return totalExceptions;
  }

  /**
   * Open and read a given number of files.
   */
  static int open() {
    int totalExceptions = 0;
    FSDataInputStream in;
    for (int index = 0; index < numFiles; index++) {
      int singleFileExceptions = 0;
      try {
        in = fileSys.open(new Path(taskDir, "" + index));
        in.close();
      } catch (IOException ioe) {
        totalExceptions++;
        handleException("opening file #" + index, ioe, ++singleFileExceptions);
      }
    }
    return totalExceptions;
  }

  /**
   * Rename a given number of files.  Repeat each remote
   * operation until is succeeds (does not throw an exception).
   *
   * @return the number of exceptions caught
   */
  static int rename() {
    int totalExceptions = 0;
    boolean success;
    for (int index = 0; index < numFiles; index++) {
      int singleFileExceptions = 0;
      do { // rename file until is succeeds
        try {
          // Possible result of this operation is at no interest to us for it
          // can return false only if the namesystem
          // could rename the path from the name
          // space (e.g. no Exception has been thrown)
          fileSys.rename(new Path(taskDir, "" + index),
                  new Path(taskDir, "A" + index));
          success = true;
        } catch (IOException ioe) {
          success = false;
          totalExceptions++;
          handleException("creating file #" + index, ioe, ++singleFileExceptions);
        }
      } while (!success);
    }
    return totalExceptions;
  }

  /**
   * Delete a given number of files.  Repeat each remote
   * operation until is succeeds (does not throw an exception).
   *
   * @return the number of exceptions caught
   */
  static int delete() {
    int totalExceptions = 0;
    boolean success;
    for (int index = 0; index < numFiles; index++) {
      int singleFileExceptions = 0;
      do { // delete file until is succeeds
        try {
          // Possible result of this operation is at no interest to us for it
          // can return false only if namesystem
          // delete could remove the path from the name
          // space (e.g. no Exception has been thrown)
          if (deleteBeforeRename) {
            fileSys.delete(new Path(taskDir, "" + index), false);
          } else {
            fileSys.delete(new Path(taskDir, "A" + index), false);
          }
          success = true;
        } catch (IOException ioe) {
          success = false;
          totalExceptions++;
          handleException("creating file #" + index, ioe, ++singleFileExceptions);
        }
      } while (!success);
    }
    return totalExceptions;
  }

  public static void main(String[] args) throws IOException {
    String version = "NameNodeBenchmark.1.0";
    System.out.println(version);

    String usage =
            "Usage: nnbench " +
                    "  -operation <one of create, read, rename, or delete>\n " +
                    "  -baseDir <base output/input DFS path>\n " +
                    "  -deleteBeforeRename \n" +
                    "  -numberOfFiles <number of files to create>\n ";

    String operation = null;
    for (int i = 0; i < args.length; i++) { // parse command line
      if (args[i].equals("-baseDir")) {
        baseDir = new Path(args[++i]);
      } else if (args[i].equals("-numberOfFiles")) {
        numFiles = Integer.parseInt(args[++i]);
      } else if (args[i].equals("-operation")) {
        operation = args[++i];
      } else if (args[i].equals("-deleteBeforeRename")) {
        deleteBeforeRename = true;
        ++i;
      }
      else {
        System.out.println(usage);
        System.exit(-1);
      }
    }

    JobConf jobConf = new JobConf(new Configuration(), NNBenchWithoutMR.class);

    System.out.println("Inputs: ");
    System.out.println("   operation: " + operation);
    System.out.println("   baseDir: " + baseDir);
    System.out.println("   numFiles: " + numFiles);

    if (operation == null ||  // verify args
            baseDir == null ||
            numFiles < 1) {
      System.err.println(usage);
      System.exit(-1);
    }

    fileSys = baseDir.getFileSystem(jobConf);
    String uniqueId = java.net.InetAddress.getLocalHost().getHostName();
    taskDir = new Path(baseDir, uniqueId);

    Date execTime;
    Date endTime;
    long duration;
    int exceptions = 0;
    execTime = new Date();
    System.out.println("Job started: " + execTime);

    if ("create".equals(operation)) {
      LOG.info("Deleting task directory");
      fileSys.delete(taskDir, true);
      if (!fileSys.mkdirs(taskDir)) {
        throw new IOException("Mkdirs failed to create " + taskDir.toString());
      }
      exceptions = create();
    } else if ("open".equals(operation)) {
      exceptions = open();
    } else if ("rename".equals(operation)) {
      exceptions = rename();
    } else if ("delete".equals(operation)) {
      exceptions = delete();
    } else {
      System.err.println(usage);
      System.exit(-1);
    }
    endTime = new Date();
    System.out.println("Job ended: " + endTime);
    System.out.println("The " + operation + " job took " + (endTime.getTime() - execTime.getTime()) + " ms.");
    System.out.println("The " + operation + " TPS " + ((int) ((double) numFiles / (endTime.getTime() - execTime.getTime()) * 1000)));
    System.out.println("The " + operation + " Latency(ms) " + String.format("%.2f", (double) (endTime.getTime() - execTime.getTime()) / numFiles));
    System.out.println("The job recorded " + exceptions + " exceptions.");
  }
}
