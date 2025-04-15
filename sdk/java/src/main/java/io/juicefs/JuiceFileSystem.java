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

import io.juicefs.utils.BgTaskUtil;
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
    boolean asBgTask = conf.getBoolean("juicefs.internal-bg-task", false);
    if (!asBgTask && !Boolean.parseBoolean(getConf(conf, "disable-trash-emptier", "false"))) {
      BgTaskUtil.startTrashEmptier(uri.getHost(), () -> {
        runTrashEmptier(uri, conf);
      }, 10, TimeUnit.MINUTES);
    }
  }

  private void runTrashEmptier(URI uri, final Configuration conf) {
    try {
      Configuration newConf = new Configuration(conf);
      newConf.setBoolean("juicefs.internal-bg-task", true);
      UserGroupInformation superUser = UserGroupInformation.createRemoteUser(getConf(conf, "superuser", "hdfs"));
      FileSystem emptierFs = superUser.doAs((PrivilegedExceptionAction<FileSystem>) () -> {
        JuiceFileSystemImpl fs = new JuiceFileSystemImpl();
        fs.initialize(uri, newConf);
        return fs;
      });
      new Trash(emptierFs, newConf).getEmptier().run();
    } catch (Exception e) {
      LOG.warn("run trash emptier for {} failed", uri.getHost(), e);
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
        (elements[2].getMethodName().equals("<init>") || elements[2].getMethodName().equals("checkSupportedFSSchemes"))) {
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
}
