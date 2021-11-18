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

import com.kenai.jffi.internal.StubLoader;
import io.juicefs.metrics.JuiceFSInstrumentation;
import io.juicefs.utils.ConsistentHash;
import io.juicefs.utils.NodesFetcher;
import io.juicefs.utils.NodesFetcherBuilder;
import jnr.ffi.LibraryLoader;
import jnr.ffi.Memory;
import jnr.ffi.Pointer;
import jnr.ffi.Runtime;
import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.classification.InterfaceStability;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsAction;
import org.apache.hadoop.fs.permission.FsPermission;
import org.apache.hadoop.io.DataOutputBuffer;
import org.apache.hadoop.io.MD5Hash;
import org.apache.hadoop.security.AccessControlException;
import org.apache.hadoop.security.UserGroupInformation;
import org.apache.hadoop.util.DataChecksum;
import org.apache.hadoop.util.DirectBufferPool;
import org.apache.hadoop.util.Progressable;
import org.apache.hadoop.util.VersionInfo;
import org.json.JSONObject;
import sun.nio.ch.DirectBuffer;

import java.io.*;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.net.*;
import java.nio.ByteBuffer;
import java.nio.file.Paths;
import java.util.*;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.jar.JarFile;
import java.util.stream.Collectors;
import java.util.zip.GZIPInputStream;

/****************************************************************
 * Implement the FileSystem API for JuiceFS
 *****************************************************************/
@InterfaceAudience.Public
@InterfaceStability.Stable
public class JuiceFileSystemImpl extends FileSystem {

  public static final Log LOG = LogFactory.getLog(JuiceFileSystemImpl.class);

  private Path workingDir;
  private String name;
  private URI uri;
  private long blocksize;
  private int minBufferSize;
  private int cacheReplica;
  private boolean fileChecksumEnabled;
  private boolean posixBehavior;
  private Libjfs lib;
  private long handle;
  private UserGroupInformation ugi;
  private String homeDirPrefix = "/user";
  private Map<String, String> cachedHosts = new HashMap<>(); // (ip, hostname)
  private ConsistentHash<String> hash = new ConsistentHash<>(1, Collections.singletonList("localhost"));
  private FsPermission uMask;
  private String hflushMethod;
  private ScheduledExecutorService nodesFetcherThread;
  private ScheduledExecutorService refreshUidThread;
  private Map<String, FileStatus> lastFileStatus = new HashMap<>();
  private static final DirectBufferPool bufferPool = new DirectBufferPool();
  private boolean metricsEnable = false;

  /*
   * hadoop compability
   */
  private boolean withStreamCapability;
  // constructor for BufferedFSOutputStreamWithStreamCapabilities
  private Constructor<?> constructor;
  private Method setStorageIds;
  private String[] storageIds;
  private Random random = new Random();
  private String callerContext;

  public static interface Libjfs {
    long jfs_init(String name, String jsonConf, String user, String group, String superuser, String supergroup);

    void jfs_update_uid_grouping(long h, String uidstr, String grouping);

    int jfs_term(long pid, long h);

    int jfs_open(long pid, long h, String path, int flags);

    int jfs_access(long pid, long h, String path, int flags);

    long jfs_lseek(long pid, int fd, long pos, int whence);

    int jfs_pread(long pid, int fd, Pointer b, int len, long offset);

    int jfs_write(long pid, int fd, Pointer b, int len);

    int jfs_flush(long pid, int fd);

    int jfs_fsync(long pid, int fd);

    int jfs_close(long pid, int fd);

    int jfs_create(long pid, long h, String path, short mode);

    int jfs_truncate(long pid, long h, String path, long length);

    int jfs_delete(long pid, long h, String path);

    int jfs_rmr(long pid, long h, String path);

    int jfs_mkdir(long pid, long h, String path, short mode);

    int jfs_rename(long pid, long h, String src, String dst);

    int jfs_symlink(long pid, long h, String target, String path);

    int jfs_readlink(long pid, long h, String path, Pointer buf, int bufsize);

    int jfs_stat1(long pid, long h, String path, Pointer buf);

    int jfs_lstat1(long pid, long h, String path, Pointer buf);

    int jfs_summary(long pid, long h, String path, Pointer buf);

    int jfs_statvfs(long pid, long h, Pointer buf);

    int jfs_chmod(long pid, long h, String path, int mode);

    int jfs_setOwner(long pid, long h, String path, String user, String group);

    int jfs_utime(long pid, long h, String path, long mtime, long atime);

    int jfs_chown(long pid, long h, String path);

    int jfs_listdir(long pid, long h, String path, int offset, Pointer buf, int size);

    int jfs_concat(long pid, long h, String path, Pointer buf, int bufsize);

    int jfs_setXattr(long pid, long h, String path, String name, Pointer value, int vlen, int mode);

    int jfs_getXattr(long pid, long h, String path, String name, Pointer buf, int size);

    int jfs_listXattr(long pid, long h, String path, Pointer buf, int size);

    int jfs_removeXattr(long pid, long h, String path, String name);
  }

  static int EPERM = -0x01;
  static int ENOENT = -0x02;
  static int EINTR = -0x04;
  static int EIO = -0x05;
  static int EACCESS = -0xd;
  static int EEXIST = -0x11;
  static int ENOTDIR = -0x14;
  static int EINVAL = -0x16;
  static int ENOSPACE = -0x1c;
  static int EROFS = -0x1e;
  static int ENOTEMPTY = -0x27;
  static int ENODATA = -0x3d;
  static int ENOATTR = -0x5d;
  static int ENOTSUP = -0x5f;

  static int MODE_MASK_R = 4;
  static int MODE_MASK_W = 2;
  static int MODE_MASK_X = 1;

  private IOException error(int errno, Path p) {
    if (errno == EPERM) {
      return new PathPermissionException(p.toString());
    } else if (errno == ENOTDIR) {
      return new ParentNotDirectoryException();
    } else if (errno == ENOENT) {
      return new FileNotFoundException(p.toString() + ": not found");
    } else if (errno == EACCESS) {
      try {
        UserGroupInformation ugi = UserGroupInformation.getCurrentUser();
        String user = ugi.getShortUserName();
        FileStatus stat = getFileStatusInternalNoException(p);
        if (stat != null) {
          FsPermission perm = stat.getPermission();
          return new AccessControlException(String.format("Permission denied: user=%s, path=\"%s\":%s:%s:%s%s", user, p,
                  stat.getOwner(), stat.getGroup(), stat.isDirectory() ? "d" : "-", perm));
        }
      } catch (Exception e) {
        LOG.warn("fail to generate better error message", e);
      }
      return new AccessControlException("Permission denied: " + p.toString());
    } else if (errno == EEXIST) {
      return new FileAlreadyExistsException();
    } else if (errno == EINVAL) {
      return new InvalidRequestException("Invalid parameter");
    } else if (errno == ENOTEMPTY) {
      return new PathIsNotEmptyDirectoryException(p.toString());
    } else if (errno == EINTR) {
      return new InterruptedIOException();
    } else if (errno == ENOTSUP) {
      return new PathOperationException(p.toString());
    } else if (errno == ENOSPACE) {
      return new IOException("No space");
    } else if (errno == EROFS) {
      return new IOException("Read-only Filesystem");
    } else if (errno == EIO) {
      return new IOException(p.toString());
    } else {
      return new IOException("errno: " + errno + " " + p.toString());
    }
  }

  public JuiceFileSystemImpl() {
  }

  @Override
  public long getDefaultBlockSize() {
    return blocksize;
  }

  private String normalizePath(Path path) {
    return makeQualified(path).toUri().getPath();
  }

  public String getScheme() {
    return uri.getScheme();
  }

  @Override
  public String toString() {
    return uri.toString();
  }

  @Override
  public URI getUri() {
    return uri;
  }

  private String getConf(Configuration conf, String key, String value) {
    String v = conf.get("juicefs." + key, value);
    if (name != null && !name.equals("")) {
      v = conf.get("juicefs." + name + "." + key, v);
    }
    if (v != null)
      v = v.trim();
    return v;
  }

  @Override
  public void initialize(URI uri, Configuration conf) throws IOException {
    super.initialize(uri, conf);
    setConf(conf);

    this.uri = uri;
    name = conf.get("juicefs.name", uri.getHost());
    if (null == name) {
      throw new IOException("name is required");
    }

    blocksize = conf.getLong("juicefs.block.size", conf.getLong("dfs.blocksize", 128 << 20));
    minBufferSize = conf.getInt("juicefs.min-buffer-size", 128 << 10);
    cacheReplica = Integer.parseInt(getConf(conf, "cache-replica", "1"));
    fileChecksumEnabled = Boolean.parseBoolean(getConf(conf, "file.checksum", "false"));

    this.ugi = UserGroupInformation.getCurrentUser();
    String user = ugi.getShortUserName();
    String group = "nogroup";
    String groupingFile = getConf(conf, "groups", null);
    if (isEmpty(groupingFile) && ugi.getGroupNames().length > 0) {
      group = String.join(",", ugi.getGroupNames());
    }
    String superuser = getConf(conf, "superuser", "hdfs");
    String supergroup = getConf(conf, "supergroup", conf.get("dfs.permissions.superusergroup", "supergroup"));
    String mountpoint = getConf(conf, "mountpoint", "");
    posixBehavior = !mountpoint.equals("");
    if (posixBehavior) {
      LOG.info("change to POSIX behavior: overwrite in rename, unlink don't follow symlink");
    }

    initCache(conf);
    refreshCache(conf);

    lib = loadLibrary();
    JSONObject obj = new JSONObject();
    String[] keys = new String[]{"meta",};
    for (String key : keys) {
      obj.put(key, getConf(conf, key, ""));
    }
    String[] bkeys = new String[]{"debug", "writeback"};
    for (String key : bkeys) {
      obj.put(key, Boolean.valueOf(getConf(conf, key, "false")));
    }
    obj.put("bucket", getConf(conf, "bucket", ""));
    obj.put("readOnly", Boolean.valueOf(getConf(conf, "read-only", "false")));
    obj.put("cacheDir", getConf(conf, "cache-dir", "memory"));
    obj.put("cacheSize", Integer.valueOf(getConf(conf, "cache-size", "100")));
    obj.put("openCache", Float.valueOf(getConf(conf, "open-cache", "0.0")));
    obj.put("attrTimeout", Float.valueOf(getConf(conf, "attr-cache", "0.0")));
    obj.put("entryTimeout", Float.valueOf(getConf(conf, "entry-cache", "0.0")));
    obj.put("dirEntryTimeout", Float.valueOf(getConf(conf, "dir-entry-cache", "0.0")));
    obj.put("cacheFullBlock", Boolean.valueOf(getConf(conf, "cache-full-block", "true")));
    obj.put("metacache", Boolean.valueOf(getConf(conf, "metacache", "true")));
    obj.put("autoCreate", Boolean.valueOf(getConf(conf, "auto-create-cache-dir", "true")));
    obj.put("maxUploads", Integer.valueOf(getConf(conf, "max-uploads", "20")));
    obj.put("uploadLimit", Integer.valueOf(getConf(conf, "upload-limit", "0")));
    obj.put("downloadLimit", Integer.valueOf(getConf(conf, "download-limit", "0")));
    obj.put("getTimeout", Integer.valueOf(getConf(conf, "get-timeout", getConf(conf, "object-timeout", "5"))));
    obj.put("putTimeout", Integer.valueOf(getConf(conf, "put-timeout", getConf(conf, "object-timeout", "60"))));
    obj.put("memorySize", Integer.valueOf(getConf(conf, "memory-size", "300")));
    obj.put("prefetch", Integer.valueOf(getConf(conf, "prefetch", "1")));
    obj.put("readahead", Integer.valueOf(getConf(conf, "max-readahead", "0")));
    obj.put("pushGateway", getConf(conf, "push-gateway", ""));
    obj.put("pushInterval", Integer.valueOf(getConf(conf, "push-interval", "10")));
    obj.put("pushAuth", getConf(conf, "push-auth", ""));
    obj.put("fastResolve", Boolean.valueOf(getConf(conf, "fast-resolve", "true")));
    obj.put("noUsageReport", Boolean.valueOf(getConf(conf, "no-usage-report", "false")));
    obj.put("freeSpace", getConf(conf, "free-space", "0.1"));
    obj.put("accessLog", getConf(conf, "access-log", ""));
    String jsonConf = obj.toString(2);
    handle = lib.jfs_init(name, jsonConf, user, group, superuser, supergroup);
    if (handle <= 0) {
      throw new IOException("JuiceFS initialized failed for jfs://" + name);
    }
    homeDirPrefix = conf.get("dfs.user.home.dir.prefix", "/user");
    this.workingDir = getHomeDirectory();

    // hadoop29 and above check
    try {
      Class.forName("org.apache.hadoop.fs.StreamCapabilities");
      withStreamCapability = true;
    } catch (ClassNotFoundException e) {
      withStreamCapability = false;
    }
    if (withStreamCapability) {
      try {
        constructor = Class.forName("io.juicefs.JuiceFileSystemImpl$BufferedFSOutputStreamWithStreamCapabilities")
                .getConstructor(OutputStream.class, Integer.TYPE, String.class);
      } catch (ClassNotFoundException | NoSuchMethodException e) {
        throw new RuntimeException(e);
      }
    }

    uMask = FsPermission.getUMask(conf);
    String umaskStr = getConf(conf, "umask", null);
    if (!isEmpty(umaskStr)) {
      uMask = new FsPermission(umaskStr);
    }

    hflushMethod = getConf(conf, "hflush", "writeback");
    initializeStorageIds(conf);

    callerContext = getConf(conf, "caller-context", null);

    if ("true".equalsIgnoreCase(getConf(conf, "enable-metrics", "false"))) {
      metricsEnable = true;
      JuiceFSInstrumentation.init(this, statistics);
    }

    String uidFile = getConf(conf, "users", null);
    if (!isEmpty(uidFile) || !isEmpty(groupingFile)) {
      updateUidAndGrouping(uidFile, groupingFile);
      refreshUidAndGrouping(uidFile, groupingFile);
    }
  }

  private boolean isEmpty(String str) {
    return str == null || str.trim().isEmpty();
  }

  private String readFile(String file) {
    Path path = new Path(file);
    URI uri = path.toUri();
    FileSystem fs;
    try {
      if (getScheme().equals(uri.getScheme()) &&
              (name != null && name.equals(uri.getAuthority()))) {
        fs = this;
      } else {
        fs = path.getFileSystem(getConf());
      }

      FileStatus lastStatus = lastFileStatus.get(file);
      FileStatus status = fs.getFileStatus(path);
      if (lastStatus != null && status.getModificationTime() == lastStatus.getModificationTime()
              && status.getLen() == lastStatus.getLen()) {
        return null;
      }
      FSDataInputStream in = fs.open(path);
      String res = new BufferedReader(new InputStreamReader(in)).lines().collect(Collectors.joining("\n"));
      in.close();
      lastFileStatus.put(file, status);
      return res;
    } catch (IOException e) {
      LOG.warn(String.format("read %s failed", file), e);
      return null;
    }
  }

  private void updateUidAndGrouping(String uidFile, String groupFile) {
    String uidstr = null;
    if (uidFile != null && !"".equals(uidFile.trim())) {
      uidstr = readFile(uidFile);
    }
    String grouping = null;
    if (groupFile != null && !"".equals(groupFile.trim())) {
      grouping = readFile(groupFile);
    }

    lib.jfs_update_uid_grouping(handle, uidstr, grouping);
  }

  private void refreshUidAndGrouping(String uidFile, String groupFile) {
    refreshUidThread = Executors.newScheduledThreadPool(1, r -> {
      Thread thread = new Thread(r, "Uid and group refresher");
      thread.setDaemon(true);
      return thread;
    });
    refreshUidThread.scheduleAtFixedRate(() -> {
      updateUidAndGrouping(uidFile, groupFile);
    }, 1, 1, TimeUnit.MINUTES);
  }

  private void initializeStorageIds(Configuration conf) throws IOException {
    try {
      Class<?> clazz = Class.forName("org.apache.hadoop.fs.BlockLocation");
      setStorageIds = clazz.getMethod("setStorageIds", String[].class);
    } catch (ClassNotFoundException e) {
      throw new IllegalStateException(
              "Hadoop version was incompatible, current hadoop version is:\t" + VersionInfo.getVersion());
    } catch (NoSuchMethodException e) {
      setStorageIds = null;
    }
    int vdiskPerCpu = Integer.parseInt(getConf(conf, "vdisk-per-cpu", "4"));
    storageIds = new String[java.lang.Runtime.getRuntime().availableProcessors() * vdiskPerCpu];
    for (int i = 0; i < storageIds.length; i++) {
      storageIds[i] = "vd" + i;
    }
  }

  @Override
  public Path getHomeDirectory() {
    return makeQualified(new Path(homeDirPrefix + "/" + ugi.getShortUserName()));
  }

  private static void initStubLoader() {
    int loadMaxTime = 30;
    long start = System.currentTimeMillis();
    Class<?> clazz = null;
    // first try
    try {
      clazz = Class.forName("com.kenai.jffi.internal.StubLoader");
    } catch (ClassNotFoundException e) {
    }

    // try try try ...
    while (StubLoader.getFailureCause() != null && (System.currentTimeMillis() - start) < loadMaxTime * 1000) {
      LOG.warn("StubLoader load failed, it'll be retried!");
      try {
        Thread.interrupted();
        Method load = clazz.getDeclaredMethod("load");
        load.setAccessible(true);
        load.invoke(null);

        Field loaded = clazz.getDeclaredField("loaded");
        loaded.setAccessible(true);
        loaded.set(null, true);

        Field failureCause = clazz.getDeclaredField("failureCause");
        failureCause.setAccessible(true);
        failureCause.set(null, null);
      } catch (Throwable e) {
      }
    }

    if (StubLoader.getFailureCause() != null) {
      throw new RuntimeException("StubLoader load failed", StubLoader.getFailureCause());
    }
  }

  public static Libjfs loadLibrary() throws IOException {
    initStubLoader();

    LibraryLoader<Libjfs> libjfsLibraryLoader = LibraryLoader.create(Libjfs.class);
    libjfsLibraryLoader.failImmediately();
    String name = "libjfs.4.so";
    File dir = new File("/tmp");
    String os = System.getProperty("os.name");
    if (os.toLowerCase().contains("windows")) {
      name = "libjfs3.dll";
      dir = new File(System.getProperty("java.io.tmpdir"));
    }
    File libFile = new File(dir, name);
    URL res = JuiceFileSystemImpl.class.getResource("/libjfs.so.gz");
    if (res == null) {
      // jar may changed
      return libjfsLibraryLoader.load(libFile.getAbsolutePath());
    }
    URLConnection conn;
    try {
      conn = res.openConnection();
    } catch (FileNotFoundException e) {
      // jar may changed
      return libjfsLibraryLoader.load(libFile.getAbsolutePath());
    }

    long soTime = conn.getLastModified();
    if (res.getProtocol().equalsIgnoreCase("jar")) {
      String jarPath = Paths.get(URI.create(res.getFile()))
              .getParent().toUri().getPath().trim();
      jarPath = jarPath.substring(0, jarPath.length() - 1);
      soTime = new JarFile(jarPath).getJarEntry("libjfs.so.gz")
              .getLastModifiedTime()
              .toMillis();
    }

    InputStream ins = conn.getInputStream();
    synchronized (JuiceFileSystemImpl.class) {
      if (!libFile.exists() || libFile.lastModified() < soTime) {
        // try the name for current user
        libFile = new File(dir, System.getProperty("user.name") + "-" + name);
        if (!libFile.exists() || libFile.lastModified() < soTime) {
          InputStream reader = new GZIPInputStream(ins);
          File tmp = File.createTempFile(name, null, dir);
          FileOutputStream writer = new FileOutputStream(tmp);
          byte[] buffer = new byte[128 << 10];
          int bytesRead = 0;
          while ((bytesRead = reader.read(buffer)) != -1) {
            writer.write(buffer, 0, bytesRead);
          }
          writer.close();
          reader.close();
          tmp.setLastModified(soTime);
          tmp.setReadable(true, false);
          new File(dir, name).delete();
          if (tmp.renameTo(new File(dir, name))) {
            // updated libjfs.so
            libFile = new File(dir, name);
          } else {
            libFile.delete();
            if (!tmp.renameTo(libFile)) {
              throw new IOException("Can't update " + libFile);
            }
          }
        }
      }
    }
    ins.close();
    return libjfsLibraryLoader.load(libFile.getAbsolutePath());
  }

  private void initCache(Configuration conf) {
    try {
      List<String> nodes = Arrays.asList(getConf(conf, "nodes", "localhost").split(","));
      if (nodes.size() == 1 && "localhost".equals(nodes.get(0))) {
        String urls = getConf(conf, "discover-nodes-url", null);
        if (urls != null) {
          List<String> newNodes = discoverNodes(urls);
          Map<String, String> newCachedHosts = new HashMap<>();
          for (String newNode : newNodes) {
            try {
              newCachedHosts.put(InetAddress.getByName(newNode).getHostAddress(), newNode);
            } catch (UnknownHostException e) {
              LOG.warn("unknown host: " + newNode);
            }
          }

          // if newCachedHosts are not changed, skip
          if (!newCachedHosts.equals(cachedHosts)) {
            List<String> ips = new ArrayList<>(newCachedHosts.keySet());
            LOG.debug("update nodes to: " + String.join(",", ips));
            this.hash = new ConsistentHash<>(100, ips);
            this.cachedHosts = newCachedHosts;
          }
        }
      }
    } catch (Throwable e) {
      LOG.warn(e);
    }
  }

  private void refreshCache(Configuration conf) {
    nodesFetcherThread = Executors.newScheduledThreadPool(1, r -> {
      Thread thread = new Thread(r, "Node fetcher");
      thread.setDaemon(true);
      return thread;
    });
    nodesFetcherThread.scheduleAtFixedRate(() -> {
      initCache(conf);
    }, 10, 10, TimeUnit.MINUTES);
  }

  private List<String> discoverNodes(String urls) {
    NodesFetcher fetcher = NodesFetcherBuilder.buildFetcher(urls, name);
    List<String> fetched = fetcher.fetchNodes(urls);
    if (fetched == null || fetched.isEmpty()) {
      return Collections.singletonList("localhost");
    } else {
      return fetched;
    }
  }

  private BlockLocation makeLocation(long code, long start, long len) {
    long index = (start + len / 2) / blocksize / 4;
    BlockLocation blockLocation;
    String[] ns = new String[cacheReplica];
    String[] hs = new String[cacheReplica];
    String host = cachedHosts.getOrDefault(hash.get(code + "-" + index), "localhost");
    ns[0] = host + ":50010";
    hs[0] = host;
    for (int i = 1; i < cacheReplica; i++) {
      String h = hash.get(code + "-" + (index + i));
      ns[i] = h + ":50010";
      hs[i] = h;
    }
    blockLocation = new BlockLocation(ns, hs, null, null, start, len, false);
    if (setStorageIds != null) {
      try {
        setStorageIds.invoke(blockLocation, (Object) getStorageIds());
      } catch (IllegalAccessException | InvocationTargetException e) {
        throw new RuntimeException(e);
      }
    }
    return blockLocation;
  }

  private String[] getStorageIds() {
    String[] res = new String[cacheReplica];
    for (int i = 0; i < cacheReplica; i++) {
      res[i] = storageIds[random.nextInt(storageIds.length)];
    }
    return res;
  }

  public BlockLocation[] getFileBlockLocations(FileStatus file, long start, long len) throws IOException {
    if (file == null) {
      return null;
    }
    if (start < 0 || len < 0) {
      throw new IllegalArgumentException("Invalid start or len parameter");
    }
    if (file.getLen() <= start) {
      return new BlockLocation[0];
    }
    if (cacheReplica <= 0) {
      String[] name = new String[]{"localhost:50010"};
      String[] host = new String[]{"localhost"};
      return new BlockLocation[]{new BlockLocation(name, host, 0L, file.getLen())};
    }
    if (file.getLen() <= start + len) {
      len = file.getLen() - start;
    }
    long code = normalizePath(file.getPath()).hashCode();
    BlockLocation[] locs = new BlockLocation[(int) (len / blocksize) + 2];
    int indx = 0;
    while (len > 0) {
      long blen = len < blocksize ? len : blocksize - start % blocksize;
      locs[indx] = makeLocation(code, start, blen);
      start += blen;
      len -= blen;
      indx++;
    }
    // merge the last block
    if (indx > 1 && locs[indx - 1].getLength() < blocksize / 10) {
      locs[indx - 2].setLength(locs[indx - 2].getLength() + locs[indx - 1].getLength());
      indx--;
    }
    // merge the first block
    if (indx > 1 && locs[0].getLength() < blocksize / 10) {
      locs[1].setOffset(locs[0].getOffset());
      locs[1].setLength(locs[0].getLength() + locs[1].getLength());
      locs = Arrays.copyOfRange(locs, 1, indx);
      indx--;
    }
    return Arrays.copyOfRange(locs, 0, indx);
  }

  /*******************************************************
   * For open()'s FSInputStream.
   *******************************************************/
  class FileInputStream extends FSInputStream implements ByteBufferReadable {
    private int fd;
    private final Path path;

    private ByteBuffer buf;
    private long position;

    public FileInputStream(Path f, int fd, int size) throws IOException {
      path = f;
      this.fd = fd;
      buf = bufferPool.getBuffer(size);
      buf.limit(0);
      position = 0;
    }

    @Override
    public synchronized long getPos() throws IOException {
      if (buf == null)
        throw new IOException("stream was closed");
      return position - buf.remaining();
    }

    @Override
    public boolean seekToNewSource(long targetPos) throws IOException {
      return false;
    }

    @Override
    public synchronized int available() throws IOException {
      if (buf == null)
        throw new IOException("stream was closed");
      return buf.remaining();
    }

    @Override
    public boolean markSupported() {
      return false;
    }

    @Override
    public void reset() throws IOException {
      throw new IOException("Mark/reset not supported");
    }

    public synchronized int read() throws IOException {
      if (buf == null)
        throw new IOException("stream was closed");
      if (!buf.hasRemaining() && !refill())
        return -1; // EOF
      assert buf.hasRemaining();
      statistics.incrementBytesRead(1);
      return buf.get() & 0xFF;
    }

    public synchronized int read(byte[] b, int off, int len) throws IOException {
      if (off < 0 || len < 0 || b.length - off < len)
        throw new IndexOutOfBoundsException();
      if (len == 0)
        return 0;
      if (buf == null)
        throw new IOException("stream was closed");
      if (!buf.hasRemaining() && len <= buf.capacity() && !refill())
        return -1; // No bytes were read before EOF.

      int read = Math.min(buf.remaining(), len);
      if (read > 0) {
        buf.get(b, off, read);
        statistics.incrementBytesRead(read);
        off += read;
        len -= read;
      }
      if (len == 0)
        return read;
      int more = read(position, b, off, len);
      if (more <= 0) {
        if (read > 0) {
          return read;
        } else {
          return -1;
        }
      }
      position += more;
      buf.position(0);
      buf.limit(0);
      return read + more;
    }

    private boolean refill() throws IOException {
      buf.clear();
      int read = read(position, buf);
      if (read <= 0) {
        buf.limit(0);
        return false; // EOF
      }
      statistics.incrementBytesRead(-read);
      buf.position(0);
      buf.limit(read);
      position += read;
      return true;
    }

    @Override
    public synchronized int read(long pos, byte[] b, int off, int len) throws IOException {
      if (len == 0)
        return 0;
      if (buf == null)
        throw new IOException("stream was closed");
      if (pos < 0)
        throw new EOFException("position is negative");
      if (b == null || off < 0 || len < 0 || b.length - off < len) {
        throw new IllegalArgumentException("arguments: " + off + " " + len);
      }
      if (len > 128 << 20) {
        len = 128 << 20;
      }
      Pointer tmp = Memory.allocate(Runtime.getRuntime(lib), len);
      int got = lib.jfs_pread(Thread.currentThread().getId(), fd, tmp, len, pos);
      if (got == 0)
        return -1;
      if (got == EINVAL)
        throw new IOException("stream was closed");
      if (got < 0)
        throw error(got, path);
      tmp.get(0, b, off, got);
      statistics.incrementBytesRead(got);
      return got;
    }

    @Override
    public synchronized int read(ByteBuffer b) throws IOException {
      if (!b.hasRemaining())
        return 0;
      if (buf == null)
        throw new IOException("stream was closed");
      if (!buf.hasRemaining() && b.remaining() <= buf.capacity() && !refill()) {
        return -1;
      }
      int got = 0;
      while (b.hasRemaining() && buf.hasRemaining()) {
        b.put(buf.get());
        got++;
      }
      statistics.incrementBytesRead(got);
      if (!b.hasRemaining())
        return got;
      int more = read(position, b);
      if (more <= 0)
        return got > 0 ? got : -1;
      position += more;
      buf.position(0);
      buf.limit(0);
      return got + more;
    }

    public synchronized int read(long pos, ByteBuffer b) throws IOException {
      if (!b.hasRemaining())
        return 0;
      int got;
      if (b.hasArray()) {
        got = read(pos, b.array(), b.position(), b.remaining());
        if (got <= 0)
          return got;
      } else {
        assert b.isDirect();
        long address = ((DirectBuffer) b).address() + b.position();
        Pointer destPtr = Runtime.getRuntime(lib).getMemoryManager().newPointer(address);
        got = lib.jfs_pread(Thread.currentThread().getId(), fd, destPtr, b.remaining(), pos);
        if (got == EINVAL)
          throw new IOException("stream was closed");
        if (got < 0)
          throw error(got, path);
        if (got == 0)
          return -1;
        statistics.incrementBytesRead(got);
      }
      b.position(b.position() + got);
      return got;
    }

    @Override
    public synchronized void seek(long p) throws IOException {
      if (p < 0) {
        throw new EOFException(FSExceptionMessages.NEGATIVE_SEEK);
      }
      if (buf == null)
        throw new IOException("stream was closed");
      if (p < position && p >= position - buf.limit()) {
        buf.position((int) (p - (position - buf.limit())));
      } else {
        buf.position(0);
        buf.limit(0);
        position = p;
      }
    }

    @Override
    public synchronized long skip(long n) throws IOException {
      if (n < 0)
        return -1;
      if (buf == null)
        throw new IOException("stream was closed");
      if (n < buf.remaining()) {
        buf.position(buf.position() + (int) n);
      } else {
        position += n - buf.remaining();
        buf.position(0);
        buf.limit(0);
      }
      return n;
    }

    @Override
    public synchronized void close() throws IOException {
      if (buf == null) {
        return; // already closed
      }
      bufferPool.returnBuffer(buf);
      buf = null;
      int r = lib.jfs_close(Thread.currentThread().getId(), fd);
      fd = 0;
      if (r < 0)
        throw error(r, path);
    }
  }

  private Map<String, FileSystem> fsCache = new HashMap<String, FileSystem>();

  private FileSystem getFileSystem(Path path) throws IOException {
    URI uri = path.toUri();
    String scheme = uri.getScheme();
    if (scheme == null) {
      return this;
    }
    String key = scheme.toLowerCase() + "://";
    String authority = uri.getAuthority();
    if (authority != null) {
      key += authority.toLowerCase();
    }
    if (callerContext != null && !"".equals(callerContext.trim())) {
      try {
        Class<?> ctx = Class.forName("io.juicefs.utils.CallerContextUtil");
        Method setContext = ctx.getDeclaredMethod("setContext", String.class);
        setContext.invoke(null, callerContext);
      } catch (ClassNotFoundException ignored) {
      } catch (Throwable e) {
        LOG.error(e.getMessage(), e);
      }
    }
    synchronized (this) {
      FileSystem fs = fsCache.get(key);
      if (fs == null) {
        fs = FileSystem.newInstance(uri, getConf());
        fsCache.put(key, fs);
      }
      return fs;
    }
  }

  @Override
  public FSDataInputStream open(Path f, int bufferSize) throws IOException {
    statistics.incrementReadOps(1);
    int fd = lib.jfs_open(Thread.currentThread().getId(), handle, normalizePath(f), MODE_MASK_R);
    if (fd < 0) {
      throw error(fd, f);
    }
    return new FSDataInputStream(new FileInputStream(f, fd, checkBufferSize(bufferSize)));
  }

  @Override
  public void access(Path path, FsAction mode) throws IOException {
    int r = lib.jfs_access(Thread.currentThread().getId(), handle, normalizePath(path), mode.ordinal());
    if (r < 0)
      throw error(r, path);
  }

  /*********************************************************
   * For create()'s FSOutputStream.
   *********************************************************/
  class FSOutputStream extends OutputStream {
    private int fd;
    private Path path;

    private FSOutputStream(int fd, Path p) throws IOException {
      this.fd = fd;
      this.path = p;
    }

    @Override
    public void close() throws IOException {
      int r = lib.jfs_close(Thread.currentThread().getId(), fd);
      if (r < 0)
        throw error(r, path);
    }

    @Override
    public void flush() throws IOException {
    }

    public void hflush() throws IOException {
      int r = lib.jfs_flush(Thread.currentThread().getId(), fd);
      if (r == EINVAL)
        throw new IOException("stream was closed");
      if (r < 0)
        throw error(r, path);
    }

    public void fsync() throws IOException {
      int r = lib.jfs_fsync(Thread.currentThread().getId(), fd);
      if (r == EINVAL)
        throw new IOException("stream was closed");
      if (r < 0)
        throw error(r, path);
    }

    @Override
    public void write(byte[] b, int off, int len) throws IOException {
      if (b.length - off < len) {
        throw new IndexOutOfBoundsException();
      }
      Pointer buf = Memory.allocate(Runtime.getRuntime(lib), len);
      buf.put(0, b, off, len);
      int done = lib.jfs_write(Thread.currentThread().getId(), fd, buf, len);
      if (done == EINVAL)
        throw new IOException("stream was closed");
      if (done < 0)
        throw error(done, path);
      if (done < len) {
        throw new IOException("write");
      }
    }

    @Override
    public void write(int b) throws IOException {
      Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 1);
      buf.putByte(0, (byte) b);
      int done = lib.jfs_write(Thread.currentThread().getId(), fd, buf, 1);
      if (done < 0)
        throw error(done, path);
      if (done < 1)
        throw new IOException("write");
    }
  }

  static class BufferedFSOutputStream extends BufferedOutputStream implements Syncable {
    private String hflushMethod;

    public BufferedFSOutputStream(OutputStream out) {
      super(out);
      hflushMethod = "writeback";
    }

    public BufferedFSOutputStream(OutputStream out, int size, String hflushMethod) {
      super(out, size);
      this.hflushMethod = hflushMethod;
    }

    public void sync() throws IOException {
      hflush();
    }

    @Override
    public void hflush() throws IOException {
      flush();
      if (hflushMethod.equals("writeback")) {
        ((FSOutputStream) out).hflush();
      } else if (hflushMethod.equals("sync") || hflushMethod.equals("fsync")) {
        ((FSOutputStream) out).fsync();
      } else {
        // nothing
      }
    }

    @Override
    public void hsync() throws IOException {
      flush();
      ((FSOutputStream) out).fsync();
    }
  }

  static class BufferedFSOutputStreamWithStreamCapabilities extends BufferedFSOutputStream
          implements StreamCapabilities {
    public BufferedFSOutputStreamWithStreamCapabilities(OutputStream out) {
      super(out);
    }

    public BufferedFSOutputStreamWithStreamCapabilities(OutputStream out, int size, String hflushMethod) {
      super(out, size, hflushMethod);
    }

    @Override
    public boolean hasCapability(String capability) {
      return capability.equalsIgnoreCase("hsync") || capability.equalsIgnoreCase(("hflush"));
    }
  }

  @Override
  public FSDataOutputStream append(Path f, int bufferSize, Progressable progress) throws IOException {
    statistics.incrementWriteOps(1);
    int fd = lib.jfs_open(Thread.currentThread().getId(), handle, normalizePath(f), MODE_MASK_W);
    if (fd < 0)
      throw error(fd, f);
    long r = lib.jfs_lseek(Thread.currentThread().getId(), fd, 0, 2);
    if (r < 0)
      throw error((int) r, f);
    return createFsDataOutputStream(f, bufferSize, fd, r);
  }

  @Override
  public FSDataOutputStream create(Path f, FsPermission permission, boolean overwrite, int bufferSize,
                                   short replication, long blockSize, Progressable progress) throws IOException {
    statistics.incrementWriteOps(1);
    while (true) {
      int fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort());
      if (fd == ENOENT) {
        Path parent = f.getParent();
        FsPermission perm = FsPermission.getDirDefault().applyUMask(FsPermission.getUMask(getConf()));
        try {
          mkdirs(parent, perm);
        } catch (FileAlreadyExistsException e) {
        }
        continue;
      }
      if (fd == EEXIST) {
        if (!overwrite || isDirectory(f)) {
          throw new FileAlreadyExistsException("File already exists: " + f);
        }
        delete(f, false);
        continue;
      }
      if (fd < 0) {
        throw error(fd, f.getParent());
      }
      return createFsDataOutputStream(f, bufferSize, fd, 0L);
    }
  }

  private int checkBufferSize(int size) {
    if (size < minBufferSize) {
      size = minBufferSize;
    }
    return size;
  }

  @Override
  public FSDataOutputStream createNonRecursive(Path f, FsPermission permission, EnumSet<CreateFlag> flag,
                                               int bufferSize, short replication, long blockSize, Progressable progress) throws IOException {
    statistics.incrementWriteOps(1);
    int fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort());
    while (fd == EEXIST) {
      if (!flag.contains(CreateFlag.OVERWRITE) || isDirectory(f)) {
        throw new FileAlreadyExistsException("File already exists: " + f);
      }
      delete(f, false);
      fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort());
    }
    if (fd < 0) {
      throw error(fd, f.getParent());
    }
    return createFsDataOutputStream(f, bufferSize, fd, 0L);
  }

  private FSDataOutputStream createFsDataOutputStream(Path f, int bufferSize, int fd, long startPosition) throws IOException {
    FSOutputStream out = new FSOutputStream(fd, f);
    if (withStreamCapability) {
      try {
        return new FSDataOutputStream(
                (OutputStream) constructor.newInstance(out, checkBufferSize(bufferSize), hflushMethod), statistics, startPosition);
      } catch (InstantiationException | IllegalAccessException | InvocationTargetException e) {
        throw new RuntimeException(e);
      }
    } else {
      return new FSDataOutputStream(new BufferedFSOutputStream(out, checkBufferSize(bufferSize), hflushMethod),
              statistics, startPosition);
    }
  }

  @Override
  public FileChecksum getFileChecksum(Path f, long length) throws IOException {
    statistics.incrementReadOps(1);
    if (!fileChecksumEnabled)
      return null;
    String combineMode = getConf().get("dfs.checksum.combine.mode", "MD5MD5CRC");
    if (!combineMode.equals("MD5MD5CRC"))
      return null;
    DataChecksum.Type ctype = DataChecksum.Type.valueOf(getConf().get("dfs.checksum.type", "CRC32C"));
    if (ctype.size != 4)
      return null;

    int bytesPerCrc = getConf().getInt("io.bytes.per.checksum", 512);
    DataChecksum summer = DataChecksum.newDataChecksum(ctype, bytesPerCrc);

    long crcPerBlock = 0;
    DataOutputBuffer checksumBuf = new DataOutputBuffer();
    DataOutputBuffer crcBuf = new DataOutputBuffer();
    byte[] buf = new byte[bytesPerCrc];
    FSDataInputStream in = open(f, 1 << 20);
    while (length > 0) {
      for (int i = 0; i < blocksize / bytesPerCrc && length > 0; i++) {
        int n;
        if (length < bytesPerCrc) {
          n = in.read(buf, 0, (int) length);
        } else {
          n = in.read(buf);
        }
        if (n <= 0) {
          length = 0; // EOF
        } else {
          summer.update(buf, 0, n);
          summer.writeValue(crcBuf, true);
          length -= n;
        }
      }
      if (crcBuf.getLength() > 0) {
        MD5Hash blockMd5 = MD5Hash.digest(crcBuf.getData(), 0, crcBuf.getLength());
        blockMd5.write(checksumBuf);
        crcBuf.reset();
        if (length > 0) { // more than one block
          crcPerBlock = blocksize / bytesPerCrc;
        }
      }
    }
    in.close();
    if (checksumBuf.getLength() == 0) { // empty file
      return new MD5MD5CRC32GzipFileChecksum(0, 0, MD5Hash.digest(new byte[32]));
    }
    MD5Hash md5 = MD5Hash.digest(checksumBuf.getData());
    if (ctype == DataChecksum.Type.CRC32C) {
      return new MD5MD5CRC32CastagnoliFileChecksum(bytesPerCrc, crcPerBlock, md5);
    } else {
      return new MD5MD5CRC32GzipFileChecksum(bytesPerCrc, crcPerBlock, md5);
    }
  }

  @Override
  public void concat(final Path dst, final Path[] srcs) throws IOException {
    statistics.incrementWriteOps(1);
    if (getFileStatus(dst).getLen() == 0) {
      throw new IOException(dst + "is empty");
    }
    for (Path src : srcs) {
      if (!exists(src)) {
        throw new FileNotFoundException(src.toString());
      }
    }
    byte[][] srcbytes = new byte[srcs.length][];
    int bufsize = 0;
    for (int i = 0; i < srcs.length; i++) {
      srcbytes[i] = normalizePath(srcs[i]).getBytes("UTF-8");
      bufsize += srcbytes[i].length + 1;
    }
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), bufsize);
    long offset = 0;
    for (int i = 0; i < srcs.length; i++) {
      buf.put(offset, srcbytes[i], 0, srcbytes[i].length);
      buf.putByte(offset + srcbytes[i].length, (byte) 0);
      offset += srcbytes[i].length + 1;
    }
    int r = lib.jfs_concat(Thread.currentThread().getId(), handle, normalizePath(dst), buf, bufsize);
    if (r < 0) {
      throw error(r, dst);
    }
    for (Path src : srcs) {
      delete(src, false);
    }
  }

  @Override
  public boolean rename(Path src, Path dst) throws IOException {
    statistics.incrementWriteOps(1);
    int r = lib.jfs_rename(Thread.currentThread().getId(), handle, normalizePath(src), normalizePath(dst));
    if (r == EEXIST) {
      if (posixBehavior) {
        FileStatus stt = getFileLinkStatus(dst);
        throw new IOException("unexpected " + stt);
      }
      try {
        FileStatus st = getFileStatus(dst);
        if (st.isDirectory()) {
          dst = new Path(dst, src.getName());
          r = lib.jfs_rename(Thread.currentThread().getId(), handle, normalizePath(src), normalizePath(dst));
        } else {
          return false;
        }
      } catch (FileNotFoundException ignored) {
      }
    }
    if (r == ENOENT || r == EEXIST)
      return false;
    if (r < 0)
      throw error(r, src);
    return true;
  }

  @Override
  public boolean truncate(Path f, long newLength) throws IOException {
    int r = lib.jfs_truncate(Thread.currentThread().getId(), handle, normalizePath(f), newLength);
    if (r < 0)
      throw error(r, f);
    return true;
  }

  private boolean rmr(Path p) throws IOException {
    FileStatus st;
    try {
      st = getFileLinkStatus(p);
    } catch (FileNotFoundException e) {
      return false;
    }
    int r = lib.jfs_rmr(Thread.currentThread().getId(), handle, normalizePath(p));
    if (r == ENOENT) {
      return false;
    }
    if (r < 0) {
      throw error(r, p);
    }
    if (posixBehavior)
      return true;
    try {
      st = getFileLinkStatus(p);
    } catch (FileNotFoundException e) {
      return true;
    }
    if (st.isSymlink()) {
      try {
        Path target = st.getSymlink();
        FileSystem fs = getFileSystem(target);
        fs.delete(target, true);
      } catch (FileNotFoundException e) {
      }
      return delete(p, false);
    }
    if (st.isDirectory()) {
      FileStatus[] children = listStatus(p);
      for (int i = 0; i < children.length; i++) {
        rmr(children[i].getPath());
      }
      return rmr(p);
    }
    return true;
  }

  @Override
  public boolean delete(Path p, boolean recursive) throws IOException {
    statistics.incrementWriteOps(1);
    if (recursive)
      return rmr(p);
    FileStatus st = null;
    if (!posixBehavior) {
      try {
        st = getFileLinkStatus(p);
      } catch (FileNotFoundException e) {
        return false;
      }
    }
    int r = lib.jfs_delete(Thread.currentThread().getId(), handle, normalizePath(p));
    if (r == ENOENT) {
      return false;
    }
    if (r < 0) {
      throw error(r, p);
    }
    if (!posixBehavior && st.isSymlink()) {
      Path target = st.getSymlink();
      try {
        FileSystem fs = getFileSystem(target);
        fs.delete(target, false);
      } catch (IllegalArgumentException ignored) {
        LOG.warn("invalid symlink " + p + " -> " + target);
      }
    }
    return true;
  }

  @Override
  public ContentSummary getContentSummary(Path f) throws IOException {
    statistics.incrementReadOps(1);
    String path = normalizePath(f);
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 24);
    int r = lib.jfs_summary(Thread.currentThread().getId(), handle, path, buf);
    if (r < 0) {
      throw error(r, f);
    }
    long size = buf.getLongLong(0);
    long files = buf.getLongLong(8);
    long dirs = buf.getLongLong(16);
    return new ContentSummary(size, files, dirs);
  }

  private FileStatus newFileStatus(Path p, Pointer buf, int size, boolean readlink) throws IOException {
    int mode = buf.getInt(0);
    boolean isdir = ((mode >>> 31) & 1) == 1; // Go
    int stickybit = (mode >>> 20) & 1;
    FsPermission perm = new FsPermission((short) ((mode & 0777) | (stickybit << 9)));
    long length = buf.getLongLong(4);
    long mtime = buf.getLongLong(12);
    long atime = buf.getLongLong(20);
    String user = buf.getString(28);
    String group = buf.getString(28 + user.length() + 1);
    assert (30 + user.length() + group.length() == size);
    if (readlink && ((mode >> 27) & 1) == 1) {
      return new FileStatus(length, isdir, 1, blocksize, mtime, atime, perm, user, group,
              getLinkTarget(p), p);
    } else {
      return new FileStatus(length, isdir, 1, blocksize, mtime, atime, perm, user, group, p);
    }
  }

  @Override
  public FileStatus[] listStatus(Path f) throws FileNotFoundException, IOException {
    statistics.incrementReadOps(1);
    int bufsize = 32 << 10;
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), bufsize); // TODO: smaller buff
    String path = normalizePath(f);
    int r = lib.jfs_listdir(Thread.currentThread().getId(), handle, path, 0, buf, bufsize);
    if (r == ENOENT) {
      throw new FileNotFoundException(f.toString());
    }
    if (r == ENOTDIR) {
      return new FileStatus[]{getFileStatus(f)};
    }

    FileStatus[] results;
    results = new FileStatus[1024];
    int j = 0;
    while (r > 0) {
      long offset = 0;
      while (offset < r) {
        int len = buf.getByte(offset) & 0xff;
        byte[] name = new byte[len];
        buf.get(offset + 1, name, 0, len);
        offset += 1 + len;
        int size = buf.getByte(offset) & 0xff;
        if (j == results.length) {
          FileStatus[] rs = new FileStatus[results.length * 2];
          System.arraycopy(results, 0, rs, 0, j);
          results = rs;
        }
        // resolve symlink to target
        Path p = makeQualified(new Path(f, new String(name)));
        FileStatus st = newFileStatus(p, buf.slice(offset + 1), size, true);
        if (st.isSymlink() && !posixBehavior) {
          Path target = st.getSymlink();
          try {
            st = getFileSystem(target).getFileStatus(target);
          } catch (IOException e) {
            LOG.warn("broken symlink " + p + " -> " + target);
          }
          st.setPath(p);
        }
        results[j] = st;
        offset += 1 + size;
        j++;
      }
      int left = buf.getInt(offset);
      if (left == 0)
        break;
      int fd = buf.getInt(offset + 4);
      r = lib.jfs_listdir(Thread.currentThread().getId(), fd, path, j, buf, bufsize);
    }
    if (r < 0) {
      throw error(r, f);
    }
    statistics.incrementReadOps(j);

    FileStatus[] sorted = Arrays.copyOf(results, j);
    Arrays.sort(sorted, (p1, p2) -> p1.getPath().compareTo(p2.getPath()));
    return sorted;
  }

  @Override
  public void setWorkingDirectory(Path newDir) {
    workingDir = fixRelativePart(newDir);
    checkPath(workingDir);
  }

  @Override
  public Path getWorkingDirectory() {
    return workingDir;
  }

  @Override
  public boolean mkdirs(Path f, FsPermission permission) throws IOException {
    statistics.incrementWriteOps(1);
    if (f == null) {
      throw new IllegalArgumentException("mkdirs path arg is null");
    }
    String path = normalizePath(f);
    if ("/".equals(path))
      return true;
    int r = lib.jfs_mkdir(Thread.currentThread().getId(), handle, path, permission.applyUMask(uMask).toShort());
    if (r == 0 || r == EEXIST && !isFile(f)) {
      return true;
    } else if (r == ENOENT) {
      Path parent = f.getParent();
      if (parent != null) {
        return mkdirs(parent, permission) && mkdirs(f, permission);
      }
    }
    throw error(r, f.getParent());
  }

  @Override
  public FileStatus getFileStatus(Path f) throws IOException {
    statistics.incrementReadOps(1);
    return getFileStatusInternal(f, true);
  }

  @Override
  public void createSymlink(final Path target, final Path link, final boolean createParent) throws IOException {
    statistics.incrementWriteOps(1);
    if (createParent) {
      Path parent = link.getParent();
      if (parent != null) {
        FsPermission perm = FsPermission.getDirDefault().applyUMask(FsPermission.getUMask(getConf()));
        mkdirs(parent, perm);
      }
    }
    String targetPath;
    try {
      targetPath = normalizePath(target);
    } catch (IllegalArgumentException e) {
      targetPath = URLDecoder.decode(target.toUri().toString());
    }
    int r = lib.jfs_symlink(Thread.currentThread().getId(), handle, targetPath, normalizePath(link));
    if (r != 0) {
      throw error(r, link.getParent());
    }
  }

  @Override
  public FileStatus getFileLinkStatus(final Path f) throws IOException {
    statistics.incrementReadOps(1);
    FileStatus st = getFileStatusInternal(f, false);
    if (st.isSymlink()) {
      Path targetQual = FSLinkResolver.qualifySymlinkTarget(getUri(), st.getPath(), st.getSymlink());
      st.setSymlink(targetQual);
    }
    return st;
  }

  private FileStatus getFileStatusInternal(final Path f, boolean dereference) throws IOException {
    String path = normalizePath(f);
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 130);
    int r;
    if (dereference) {
      r = lib.jfs_stat1(Thread.currentThread().getId(), handle, path, buf);
    } else {
      r = lib.jfs_lstat1(Thread.currentThread().getId(), handle, path, buf);
    }
    if (r < 0) {
      throw error(r, f);
    }
    return newFileStatus(makeQualified(f), buf, r, !dereference);
  }

  private FileStatus getFileStatusInternalNoException(final Path f) throws IOException {
    String path = normalizePath(f);
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 130);
    int r = lib.jfs_lstat1(Thread.currentThread().getId(), handle, path, buf);
    if (r < 0) {
      return null;
    }
    return newFileStatus(makeQualified(f), buf, r, false);
  }

  @Override
  public boolean supportsSymlinks() {
    return false;
  }

  @Override
  public String getCanonicalServiceName() {
    return null; // Does not support Token
  }

  @Override
  public Path getLinkTarget(Path f) throws IOException {
    statistics.incrementReadOps(1);
    Pointer tbuf = Memory.allocate(Runtime.getRuntime(lib), 4096);
    int r = lib.jfs_readlink(Thread.currentThread().getId(), handle, normalizePath(f), tbuf, 4096);
    if (r < 0) {
      throw error(r, f);
    }
    Path target = new Path(tbuf.getString(0));
    Path fullTarget = FSLinkResolver.qualifySymlinkTarget(getUri(), f, target);
    return fullTarget;
  }

  @Override
  public FsStatus getStatus(Path p) throws IOException {
    statistics.incrementReadOps(1);
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 16);
    int r = lib.jfs_statvfs(Thread.currentThread().getId(), handle, buf);
    if (r != 0)
      throw error(r, p);
    long capacity = buf.getLongLong(0);
    long remaining = buf.getLongLong(8);
    return new FsStatus(capacity, capacity - remaining, remaining);
  }

  @Override
  public void setPermission(Path p, FsPermission permission) throws IOException {
    statistics.incrementWriteOps(1);
    int r = lib.jfs_chmod(Thread.currentThread().getId(), handle, normalizePath(p), permission.toShort());
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void setOwner(Path p, String username, String groupname) throws IOException {
    statistics.incrementWriteOps(1);
    int r = lib.jfs_setOwner(Thread.currentThread().getId(), handle, normalizePath(p), username, groupname);
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void setTimes(Path p, long mtime, long atime) throws IOException {
    statistics.incrementWriteOps(1);
    int r = lib.jfs_utime(Thread.currentThread().getId(), handle, normalizePath(p), mtime >= 0 ? mtime : -1,
            atime >= 0 ? atime : -1);
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void close() throws IOException {
    super.close();
    if (refreshUidThread != null) {
      refreshUidThread.shutdownNow();
    }
    lib.jfs_term(Thread.currentThread().getId(), handle);
    if (nodesFetcherThread != null) {
      nodesFetcherThread.shutdownNow();
    }
    if (metricsEnable) {
      JuiceFSInstrumentation.close();
    }
  }

  public void setXAttr(Path path, String name, byte[] value, EnumSet<XAttrSetFlag> flag) throws IOException {
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), value.length);
    buf.put(0, value, 0, value.length);
    int mode = 0; // create or replace
    if (flag.contains(XAttrSetFlag.CREATE) && flag.contains(XAttrSetFlag.REPLACE)) {
      mode = 0;
    } else if (flag.contains(XAttrSetFlag.CREATE)) {
      mode = 1;
    } else if (flag.contains(XAttrSetFlag.REPLACE)) {
      mode = 2;
    }
    int r = lib.jfs_setXattr(Thread.currentThread().getId(), handle, normalizePath(path), name, buf, value.length,
            mode);
    if (r < 0)
      throw error(r, path);
  }

  public byte[] getXAttr(Path path, String name) throws IOException {
    Pointer buf;
    int bufsize = 16 << 10;
    int r;
    do {
      bufsize *= 2;
      buf = Memory.allocate(Runtime.getRuntime(lib), bufsize);
      r = lib.jfs_getXattr(Thread.currentThread().getId(), handle, normalizePath(path), name, buf, bufsize);
    } while (r == bufsize);
    if (r == ENOATTR || r == ENODATA)
      return null; // attr not found
    if (r < 0)
      throw error(r, path);
    byte[] value = new byte[r];
    buf.get(0, value, 0, r);
    return value;
  }

  public Map<String, byte[]> getXAttrs(Path path) throws IOException {
    return getXAttrs(path, listXAttrs(path));
  }

  public Map<String, byte[]> getXAttrs(Path path, List<String> names) throws IOException {
    Map<String, byte[]> result = new HashMap<String, byte[]>();
    for (String n : names) {
      byte[] value = getXAttr(path, n);
      if (value != null) {
        result.put(n, value);
      }
    }
    return result;
  }

  public List<String> listXAttrs(Path path) throws IOException {
    Pointer buf;
    int bufsize = 1024;
    int r;
    do {
      bufsize *= 2;
      buf = Memory.allocate(Runtime.getRuntime(lib), bufsize);
      r = lib.jfs_listXattr(Thread.currentThread().getId(), handle, normalizePath(path), buf, bufsize);
    } while (r == bufsize);
    if (r < 0)
      throw error(r, path);
    List<String> result = new ArrayList<String>();
    int off = 0, last = 0;
    while (off < r) {
      if (buf.getByte(off) == 0) {
        byte[] arr = new byte[off - last];
        buf.get(last, arr, 0, arr.length);
        result.add(new String(arr));
        last = off + 1;
      }
      off++;
    }
    return result;
  }

  public void removeXAttr(Path path, String name) throws IOException {
    int r = lib.jfs_removeXattr(Thread.currentThread().getId(), handle, normalizePath(path), name);
    if (r < 0)
      throw error(r, path);
  }
}
