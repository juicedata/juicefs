/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package io.juicefs;

import io.juicefs.utils.PatchUtil;
import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.classification.InterfaceStability;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.xeustechnologies.jcl.JarClassLoader;
import org.xeustechnologies.jcl.JclObjectFactory;
import org.xeustechnologies.jcl.JclUtils;

import java.io.IOException;
import java.net.URI;
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
  private static JarClassLoader jcl;

  private static boolean fileChecksumEnabled = false;
  private static boolean distcpPatched = false;

  private ScheduledExecutorService emptier;

  static {
    jcl = new JarClassLoader();
    String path = JuiceFileSystem.class.getProtectionDomain().getCodeSource().getLocation().getPath();
    jcl.add(path); // Load jar file

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

  private static FileSystem createInstance() {
    // Create default factory
    JclObjectFactory factory = JclObjectFactory.getInstance();
    Object obj = factory.create(jcl, "io.juicefs.JuiceFileSystemImpl");
    return (FileSystem) JclUtils.deepClone(obj);
  }

  @Override
  public void initialize(URI uri, Configuration conf) throws IOException {
    super.initialize(uri, conf);
    fileChecksumEnabled = Boolean.parseBoolean(getConf(conf, "file.checksum", "false"));
    startTrashEmptier(conf);
  }

  private void startTrashEmptier(final Configuration conf) throws IOException {

    emptier = Executors.newScheduledThreadPool(1, r -> {
      Thread t = new Thread(r, "Trash Emptier");
      t.setDaemon(true);
      return t;
    });

    emptier.schedule(new Trash(this, conf).getEmptier(), 10, TimeUnit.MINUTES);
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
    super(createInstance());
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
