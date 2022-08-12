/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package io.juicefs;

import io.juicefs.utils.PatchUtil;
import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.classification.InterfaceStability;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsPermission;
import org.apache.hadoop.security.UserGroupInformation;
import org.apache.hadoop.util.Progressable;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.URI;
import java.security.PrivilegedExceptionAction;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/****************************************************************
 * Implement the FileSystem API for JuiceFS
 *****************************************************************/
@InterfaceAudience.Public
@InterfaceStability.Stable
public class JuiceFileSystem extends FilterFileSystem {
  private static final Logger LOG = LoggerFactory.getLogger(JuiceFileSystem.class);

  private static boolean fileChecksumEnabled = false;
  private static boolean distcpPatched = false;

  private ScheduledExecutorService emptier;

  static {
    PatchUtil.patchBefore("org.apache.flink.runtime.fs.hdfs.HadoopRecoverableFsDataOutputStream",
            "waitUntilLeaseIsRevoked",
            new String[]{"org.apache.hadoop.fs.FileSystem", "org.apache.hadoop.fs.Path"},
            "if (fs instanceof io.juicefs.JuiceFileSystem) {\n" +
                    "            return ((io.juicefs.JuiceFileSystem)fs).isFileClosed(path);\n" +
                    "        }");
  }

  private synchronized static void patchDistCpChecksum() {
    if (distcpPatched)
      return;
    PatchUtil.patchBefore("org.apache.hadoop.tools.mapred.RetriableFileCopyCommand",
            "compareCheckSums",
            null,
            "if (sourceFS.getFileStatus(source).getBlockSize() != targetFS.getFileStatus(target).getBlockSize()) {return ;}");
    distcpPatched = true;
  }

  @Override
  public void initialize(URI uri, Configuration conf) throws IOException {
    super.initialize(uri, conf);
    fileChecksumEnabled = Boolean.parseBoolean(getConf(conf, "file.checksum", "false"));
    startTrashEmptier(uri, conf);
  }

  private void startTrashEmptier(URI uri, final Configuration conf) throws IOException {
    emptier = Executors.newScheduledThreadPool(1, r -> {
      Thread t = new Thread(r, "Trash Emptier");
      t.setDaemon(true);
      return t;
    });

    try {
      UserGroupInformation superUser = UserGroupInformation.createRemoteUser(getConf(conf, "superuser", "hdfs"));
      FileSystem emptierFs = superUser.doAs((PrivilegedExceptionAction<FileSystem>) () -> {
        JuiceFileSystemImpl fs = new JuiceFileSystemImpl();
        fs.initialize(uri, conf);
        return fs;
      });
      emptier.schedule(new Trash(emptierFs, conf).getEmptier(), 10, TimeUnit.MINUTES);
    } catch (Exception e) {
      throw new IOException("start trash failed!",e);
    }
  }

  private String getConf(Configuration conf, String key, String value) {
    String name = fs.getUri().getHost();
    String v = conf.get("juicefs." + key, value);
    if (name != null && !name.equals("")) {
      v = conf.get("juicefs." + name + "." + key, v);
    }
    if (v != null)
      v = v.trim();
    return v;
  }

  public JuiceFileSystem() {
    super(new JuiceFileSystemImpl());
  }

  @Override
  public String getScheme() {
    StackTraceElement[] elements = Thread.currentThread().getStackTrace();
    if (elements[2].getClassName().equals("org.apache.flink.runtime.fs.hdfs.HadoopRecoverableWriter") &&
            elements[2].getMethodName().equals("<init>")) {
      return "hdfs";
    }
    return fs.getScheme();
  }

  public FSDataOutputStream create(Path f, boolean overwrite, int bufferSize, short replication, long blockSize, Progressable progress) throws IOException {
    return fs.create(f, FsPermission.getFileDefault(), overwrite, bufferSize, replication, blockSize, progress);
  }

  public FSDataOutputStream createNonRecursive(Path f, boolean overwrite, int bufferSize, short replication, long blockSize, Progressable progress) throws IOException {
    return fs.createNonRecursive(f, FsPermission.getFileDefault(), overwrite, bufferSize, replication, blockSize, progress);
  }

  @Override
  public ContentSummary getContentSummary(Path f) throws IOException {
    return fs.getContentSummary(f);
  }

  public boolean isFileClosed(final Path src) throws IOException {
    FileStatus st = fs.getFileStatus(src);
    return st.getLen() > 0;
  }

  @Override
  public FileChecksum getFileChecksum(Path f, long length) throws IOException {
    if (!fileChecksumEnabled)
      return null;
    patchDistCpChecksum();
    return super.getFileChecksum(f, length);
  }

  @Override
  public FileChecksum getFileChecksum(Path f) throws IOException {
    if (!fileChecksumEnabled)
      return null;
    patchDistCpChecksum();
    return super.getFileChecksum(f);
  }

  @Override
  public void close() throws IOException {
    if (this.emptier != null) {
      emptier.shutdownNow();
    }
    super.close();
  }
}
