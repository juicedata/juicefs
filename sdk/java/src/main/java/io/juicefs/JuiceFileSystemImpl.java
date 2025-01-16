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

import com.google.common.collect.Lists;
import com.kenai.jffi.internal.StubLoader;
import io.juicefs.exception.QuotaExceededException;
import io.juicefs.metrics.JuiceFSInstrumentation;
import io.juicefs.permission.RangerConfig;
import io.juicefs.permission.RangerPermissionChecker;
import io.juicefs.utils.*;
import jnr.ffi.LibraryLoader;
import jnr.ffi.Memory;
import jnr.ffi.Pointer;
import jnr.ffi.Runtime;
import jnr.ffi.annotations.Delegate;
import jnr.ffi.annotations.In;
import jnr.ffi.annotations.Out;
import org.apache.hadoop.HadoopIllegalArgumentException;
import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.classification.InterfaceStability;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.*;
import org.apache.hadoop.io.DataOutputBuffer;
import org.apache.hadoop.io.MD5Hash;
import org.apache.hadoop.security.AccessControlException;
import org.apache.hadoop.security.UserGroupInformation;
import org.apache.hadoop.util.DataChecksum;
import org.apache.hadoop.util.DirectBufferPool;
import org.apache.hadoop.util.Progressable;
import org.apache.hadoop.util.VersionInfo;
import org.json.JSONObject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.*;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.net.*;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.nio.file.StandardCopyOption;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.jar.JarFile;
import java.util.stream.Collectors;
import java.util.zip.GZIPInputStream;
import java.util.zip.ZipEntry;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/****************************************************************
 * Implement the FileSystem API for JuiceFS
 *****************************************************************/
@InterfaceAudience.Public
@InterfaceStability.Stable
public class JuiceFileSystemImpl extends FileSystem {

  public static final Logger LOG = LoggerFactory.getLogger(JuiceFileSystemImpl.class);
  public static final String gitVer = loadVersion();

  static String loadVersion() {
    try (InputStream in = JuiceFileSystemImpl.class.getClassLoader().getResourceAsStream("juicefs-ver.properties")) {
      Properties prop = new Properties();
      prop.load(in);
      return prop.getProperty("git.commit.id.abbrev");
    } catch (IOException e) {
      LOG.warn("Failed to load juicefs-ver.properties", e);
      return "unknown";
    }
  }

  private Path workingDir;
  private String name;
  private String user;
  private String superuser;
  private String supergroup;
  private URI uri;
  private long blocksize;
  private int minBufferSize;
  private int cacheReplica;
  private boolean fileChecksumEnabled;
  private final boolean isSuperGroupFileSystem;
  private boolean isBackGroundTask = false;

  private JuiceFileSystemImpl superGroupFileSystem;
  private RangerPermissionChecker rangerPermissionChecker;
  private static Libjfs lib = loadLibrary();

  private long handle;
  private UserGroupInformation ugi;
  private String homeDirPrefix = "/user";
  private String discoverNodesUrl;
  private static final Map<String, Map<String, String>> cachedHostsForName = new ConcurrentHashMap<>(); // (name -> (ip -> hostname))
  private static final Map<String, ConsistentHash<String>> hashForName = new ConcurrentHashMap<>(); // (name -> consistentHash)
  private static final Map<String, FileStatus> lastFileStatus = new ConcurrentHashMap<>();

  private FsPermission uMask;
  private String hflushMethod;

  private static final DirectBufferPool directBufferPool = new DirectBufferPool();

  private boolean metricsEnable = false;

  /*
   * hadoop compatibility
   */
  private boolean withStreamCapability;
  private Constructor<FileStatus> fileStatusConstructor;

  // constructor for BufferedFSOutputStreamWithStreamCapabilities
  private Constructor<?> constructor;
  private Method setStorageIds;
  private String[] storageIds;
  private Random random = new Random();

  private static final String USERNAME_UID_PATTERN = "[a-zA-Z0-9_-]+:[0-9]+";
  private static final String GROUPNAME_GID_USERNAMES_PATTERN = "[a-zA-Z0-9_-]+:[0-9]+:[,a-zA-Z0-9_-]+";

  /*
    go call back
  */
  private static Libjfs.LogCallBack callBack;

  public static interface Libjfs {
    long jfs_init(String name, String jsonConf, String user, String group, String superuser, String supergroup);

    void jfs_update_uid_grouping(String name, String uidstr, String grouping);

    int jfs_term(long pid, long h);

    int jfs_open(long pid, long h, String path, @Out ByteBuffer fileLen, int flags);

    int jfs_access(long pid, long h, String path, int flags);

    long jfs_lseek(long pid, int fd, long pos, int whence);

    int jfs_pread(long pid, int fd, @Out ByteBuffer b, int len, long offset);

    int jfs_write(long pid, int fd, @In ByteBuffer b, int len);

    int jfs_flush(long pid, int fd);

    int jfs_fsync(long pid, int fd);

    int jfs_close(long pid, int fd);

    int jfs_create(long pid, long h, String path, short mode, short umask);

    int jfs_truncate(long pid, long h, String path, long length);

    int jfs_delete(long pid, long h, String path);

    int jfs_rmr(long pid, long h, String path);

    int jfs_mkdir(long pid, long h, String path, short mode, short umask);

    int jfs_rename(long pid, long h, String src, String dst);

    int jfs_stat1(long pid, long h, String path, Pointer buf);

    int jfs_lstat1(long pid, long h, String path, Pointer buf);

    int jfs_summary(long pid, long h, String path, Pointer buf);

    int jfs_statvfs(long pid, long h, Pointer buf);

    int jfs_chmod(long pid, long h, String path, int mode);

    int jfs_setOwner(long pid, long h, String path, String user, String group);

    int jfs_utime(long pid, long h, String path, long mtime, long atime);

    int jfs_listdir(long pid, long h, String path, int offset, Pointer buf, int size);

    int jfs_concat(long pid, long h, String path, Pointer buf, int bufsize);

    int jfs_setXattr(long pid, long h, String path, String name, Pointer value, int vlen, int mode);

    int jfs_getXattr(long pid, long h, String path, String name, Pointer buf, int size);

    int jfs_listXattr(long pid, long h, String path, Pointer buf, int size);

    int jfs_removeXattr(long pid, long h, String path, String name);

    int jfs_getfacl(long pid, long h, String path, int acltype, Pointer b, int len);

    int jfs_setfacl(long pid, long h, String path, int acltype, Pointer b, int len);

    String jfs_getGroups(String volName, String user);

    void jfs_set_callback(LogCallBack callBack);

    interface LogCallBack {
      @Delegate
      void call(String msg);
    }
  }

  static class LogCallBackImpl implements Libjfs.LogCallBack {
    Libjfs lib;

    public LogCallBackImpl(Libjfs lib) {
      this.lib = lib;
    }

    @Override
    public void call(String msg){
      try {
        // 2022/12/20 14:48:30.808303 juicefs[80976] <ERROR>: error msg [main.go:357]
        msg = msg.trim();
        String[] items = msg.split("\\s+", 5);
        if (items.length > 4) {
          switch (items[3]) {
            case "<DEBUG>:":
              LOG.debug(msg);
              break;
            case "<INFO>:":
              LOG.info(msg);
              break;
            case "<WARNING>:":
              LOG.warn(msg);
              break;
            case "<ERROR>:":
              LOG.error(msg);
              break;
          }
        }
      } catch (Throwable ignored){}
    }

    @Override
    protected void finalize() throws Throwable {
      lib.jfs_set_callback(null);
    }
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
  static int EDQUOT = -0x45;
  static int EROFS = -0x1e;
  static int ENOTEMPTY = -0x27;
  static int ENODATA = -0x3d;
  static int ENOATTR = -0x5d;
  static int ENOTSUP = -0x5f;

  static int MODE_MASK_R = 4;
  static int MODE_MASK_W = 2;
  static int MODE_MASK_X = 1;

  private IOException error(int errno, Path p) {
    String pStr = p == null ? "" : p.toString();
    if (errno == EPERM) {
      return new PathPermissionException(pStr);
    } else if (errno == ENOTDIR) {
      return new ParentNotDirectoryException();
    } else if (errno == ENOENT) {
      return new FileNotFoundException(pStr+ ": not found");
    } else if (errno == EACCESS) {
      try {
        FileStatus stat = getFileStatusInternalNoException(p);
        if (stat != null) {
          FsPermission perm = stat.getPermission();
          return new AccessControlException(String.format("Permission denied: user=%s, path=\"%s\":%s:%s:%s%s", user, p,
                  stat.getOwner(), stat.getGroup(), stat.isDirectory() ? "d" : "-", perm));
        }
      } catch (Exception e) {
        LOG.warn("fail to generate better error message", e);
      }
      return new AccessControlException("Permission denied: " + pStr);
    } else if (errno == EEXIST) {
      return new FileAlreadyExistsException();
    } else if (errno == EINVAL) {
      return new InvalidRequestException("Invalid parameter");
    } else if (errno == ENOTEMPTY) {
      return new PathIsNotEmptyDirectoryException(pStr);
    } else if (errno == EINTR) {
      return new InterruptedIOException();
    } else if (errno == ENOTSUP) {
      return new PathOperationException(pStr);
    } else if (errno == ENOSPACE) {
      return new IOException("No space");
    } else if (errno == EDQUOT) {
      return new QuotaExceededException("Quota exceeded");
    } else if (errno == EROFS) {
      return new IOException("Read-only Filesystem");
    } else if (errno == EIO) {
      return new IOException(pStr);
    } else {
      return new IOException("errno: " + errno + " " + pStr);
    }
  }

  public JuiceFileSystemImpl() {
    this.isSuperGroupFileSystem = false;
  }

  @Override
  public long getDefaultBlockSize() {
    return blocksize;
  }

  private String normalizePath(Path path) {
    return makeQualified(path).toUri().getPath();
  }

  @Override
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

    blocksize = conf.getLongBytes("juicefs.block.size", conf.getLongBytes("dfs.blocksize", 128 << 20));
    minBufferSize = conf.getInt("juicefs.min-buffer-size", 128 << 10);
    cacheReplica = Integer.parseInt(getConf(conf, "cache-replica", "1"));
    fileChecksumEnabled = Boolean.parseBoolean(getConf(conf, "file.checksum", "false"));

    this.ugi = UserGroupInformation.getCurrentUser();
    user = ugi.getShortUserName();
    String groupStr = "nogroup";
    if (ugi.getGroupNames().length > 0) {
      groupStr = String.join(",", ugi.getGroupNames());
    }
    superuser = getConf(conf, "superuser", "hdfs");
    supergroup = getConf(conf, "supergroup", conf.get("dfs.permissions.superusergroup", "supergroup"));
    isBackGroundTask = conf.getBoolean("juicefs.internal-bg-task", false);
    if (isSuperGroupFileSystem || isBackGroundTask) {
      groupStr = supergroup;
    }

    synchronized (JuiceFileSystemImpl.class) {
      if (callBack == null) {
        callBack = new LogCallBackImpl(lib);
        lib.jfs_set_callback(callBack);
      }
    }

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
    obj.put("storageClass", getConf(conf, "storage-class", ""));
    obj.put("readOnly", Boolean.valueOf(getConf(conf, "read-only", "false")));
    obj.put("noSession", Boolean.valueOf(getConf(conf, "no-session", "false")));
    obj.put("noBGJob", Boolean.valueOf(getConf(conf, "no-bgjob", "false")));
    obj.put("cacheDir", getConf(conf, "cache-dir", "memory"));
    obj.put("cacheSize", getConf(conf, "cache-size", "100"));
    obj.put("cacheItems", Integer.valueOf(getConf(conf, "cache-items", "0")));
    obj.put("openCache", getConf(conf, "open-cache", "0.0"));
    obj.put("backupMeta", getConf(conf, "backup-meta", "3600"));
    obj.put("backupSkipTrash", Boolean.valueOf(getConf(conf, "backup-skip-trash", "false")));
    obj.put("heartbeat", getConf(conf, "heartbeat", "12"));
    obj.put("attrTimeout", getConf(conf, "attr-cache", "0.0"));
    obj.put("entryTimeout", getConf(conf, "entry-cache", "0.0"));
    obj.put("dirEntryTimeout", getConf(conf, "dir-entry-cache", "0.0"));
    obj.put("cacheFullBlock", Boolean.valueOf(getConf(conf, "cache-full-block", "true")));
    obj.put("cacheChecksum", getConf(conf, "verify-cache-checksum", "full"));
    obj.put("cacheEviction", getConf(conf, "cache-eviction", "2-random"));
    obj.put("cacheScanInterval", getConf(conf, "cache-scan-interval", "300"));
    obj.put("cacheExpire", getConf(conf, "cache-expire", "0"));
    obj.put("autoCreate", Boolean.valueOf(getConf(conf, "auto-create-cache-dir", "true")));
    obj.put("maxUploads", Integer.valueOf(getConf(conf, "max-uploads", "20")));
    obj.put("maxDeletes", Integer.valueOf(getConf(conf, "max-deletes", "10")));
    obj.put("skipDirNlink", Integer.valueOf(getConf(conf, "skip-dir-nlink", "20")));
    obj.put("skipDirMtime", getConf(conf, "skip-dir-mtime", "100ms"));
    obj.put("uploadLimit", getConf(conf, "upload-limit", "0"));
    obj.put("downloadLimit", getConf(conf, "download-limit", "0"));
    obj.put("ioRetries", Integer.valueOf(getConf(conf, "io-retries", "10")));
    obj.put("getTimeout", getConf(conf, "get-timeout", getConf(conf, "object-timeout", "5")));
    obj.put("putTimeout", getConf(conf, "put-timeout", getConf(conf, "object-timeout", "60")));
    obj.put("memorySize", getConf(conf, "memory-size", "300"));
    obj.put("prefetch", Integer.valueOf(getConf(conf, "prefetch", "1")));
    obj.put("readahead", getConf(conf, "max-readahead", "0"));
    obj.put("pushGateway", getConf(conf, "push-gateway", ""));
    obj.put("pushInterval", getConf(conf, "push-interval", "10"));
    obj.put("pushAuth", getConf(conf, "push-auth", ""));
    obj.put("pushLabels", getConf(conf, "push-labels", ""));
    obj.put("pushGraphite", getConf(conf, "push-graphite", ""));
    obj.put("fastResolve", Boolean.valueOf(getConf(conf, "fast-resolve", "true")));
    obj.put("noUsageReport", Boolean.valueOf(getConf(conf, "no-usage-report", "false")));
    obj.put("freeSpace", getConf(conf, "free-space", "0.1"));
    obj.put("accessLog", getConf(conf, "access-log", ""));
    String jsonConf = obj.toString(2);
    handle = lib.jfs_init(name, jsonConf, user, groupStr, superuser, supergroup);
    if (handle <= 0) {
      throw new IOException("JuiceFS initialized failed for jfs://" + name);
    }
    if (isBackGroundTask) {
      LOG.debug("background fs {}|({})", name, handle);
    } else {
      BgTaskUtil.register(name, handle);
    }
    discoverNodesUrl = getConf(conf, "discover-nodes-url", null);
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
    // for hadoop compatibility
    boolean hasAclMtd = ReflectionUtil.hasMethod(FileStatus.class.getName(), "hasAcl", (String[]) null);
    if (hasAclMtd) {
      fileStatusConstructor = ReflectionUtil.getConstructor(FileStatus.class,
          long.class, boolean.class, int.class, long.class, long.class,
          long.class, FsPermission.class, String.class, String.class, Path.class,
          Path.class, boolean.class, boolean.class, boolean.class);
      if (fileStatusConstructor == null) {
        throw new IOException("incompatible hadoop version");
      }
    }

    String umaskStr = getConf(conf, "umask", null);
    if (!isEmpty(umaskStr)) {
      conf.set("fs.permissions.umask-mode", umaskStr);
      LOG.debug("override fs.permissions.umask-mode to {}", umaskStr);
    }
    uMask = FsPermission.getUMask(conf);

    hflushMethod = getConf(conf, "hflush", "writeback");
    initializeStorageIds(conf);

    if ("true".equalsIgnoreCase(getConf(conf, "enable-metrics", "false"))) {
      metricsEnable = true;
      JuiceFSInstrumentation.init(this, statistics);
    }


    String rangerRestUrl = getConf(conf, "ranger-rest-url", null);
    if (!isEmpty(rangerRestUrl) && !isSuperGroupFileSystem && !isBackGroundTask) {
        RangerConfig rangerConfig = checkAndGetRangerParams(rangerRestUrl, conf);
        Configuration superConf = new Configuration(conf);
        superConf.set("juicefs.internal-bg-task", "true");
        superGroupFileSystem = new JuiceFileSystemImpl(true);
        superGroupFileSystem.initialize(uri, superConf);
        rangerPermissionChecker = RangerPermissionChecker.acquire(name, handle, superGroupFileSystem, rangerConfig);
    }

    if (!isBackGroundTask && !isSuperGroupFileSystem) {
      // use juicefs.users and juicefs.groups for global mapping
      String uidFile = getConf(conf, "users", null);
      String groupFile = getConf(conf, "groups", null);
      if (!isEmpty(uidFile) || !isEmpty(groupFile)) {
        BgTaskUtil.putTask(name, "Refresh guid", () -> {
          updateUidAndGrouping(uidFile, groupFile);
        }, 1, 1, TimeUnit.MINUTES);
      }
    }
  }

  private RangerConfig checkAndGetRangerParams(String rangerRestUrl, Configuration conf) throws IOException {
    if (!rangerRestUrl.startsWith("http")) {
      throw new IOException("illegal value for parameter 'juicefs.ranger-rest-url': " + rangerRestUrl);
    }

    String serviceName = getConf(conf, "ranger-service-name", "");
    if (serviceName.isEmpty()) {
      throw new IOException("illegal value for parameter 'juicefs.ranger-service-name': " + serviceName);
    }

    String pollIntervalMs = getConf(conf, "ranger-poll-interval-ms", "30000");

    return new RangerConfig(rangerRestUrl, serviceName, Long.parseLong(pollIntervalMs));
  }

  private JuiceFileSystemImpl(boolean isSuperGroupFileSystem) {
    this.isSuperGroupFileSystem = isSuperGroupFileSystem;
  }

  private Set<String> getGroups() {
    String groupsFile = getConf(getConf(), "groups", null);
    if (isEmpty(groupsFile)) {
      return new HashSet<>(ugi.getGroups());
    }
    String gStr = lib.jfs_getGroups(name, user);
    Set<String> res;
    if (!isEmpty(gStr)) {
      res = new HashSet<>(Arrays.asList(gStr.split(","))) ;
    } else {
      res = new HashSet<>(ugi.getGroups());
    }
    return res;
  }

  private boolean hasSuperPermission() {
    return user.equals(superuser) || getGroups().contains(supergroup);
  }

  private boolean needCheckPermission() {
    return rangerPermissionChecker != null && !isSuperGroupFileSystem && !isBackGroundTask && !hasSuperPermission() ;
  }

  private boolean checkPathAccess(Path path, FsAction action, String operation) throws IOException {
    return rangerPermissionChecker.checkPermission(path, false, null, null, action, operation, user, getGroups());
  }

  private boolean checkParentPathAccess(Path path, FsAction action, String operation) throws IOException {
    return rangerPermissionChecker.checkPermission(path, false, null, action, null, operation, user, getGroups());
  }

  private boolean checkAncestorAccess(Path path, FsAction action, String operation) throws IOException {
    return rangerPermissionChecker.checkPermission(path, false, action, null, null, operation, user, getGroups());
  }

  private boolean checkOwner(Path path, String operation) throws IOException {
    return rangerPermissionChecker.checkPermission(path, true, null, null, null, operation, user, getGroups());
  }

  private boolean isEmpty(String str) {
    return str == null || str.trim().isEmpty();
  }

  private String readFile(String file) throws IOException {
    Path path = new Path(file);
    FileStatus lastStatus = lastFileStatus.get(file);
    Configuration newConf = new Configuration(getConf());
    newConf.setBoolean("juicefs.internal-bg-task", true);
    try (FileSystem fs = FileSystem.newInstance(path.toUri(), newConf)) {
      FileStatus status = fs.getFileStatus(path);
      if (lastStatus != null && status.getModificationTime() == lastStatus.getModificationTime()
          && status.getLen() == lastStatus.getLen()) {
        return null;
      }
      try (FSDataInputStream in = fs.open(path)) {
        String res = new BufferedReader(new InputStreamReader(in)).lines().collect(Collectors.joining("\n"));
        lastFileStatus.put(file, status);
        return res;
      }
    }
  }

  private String parseUidAndGrouping(String pattern, String input) {
    String result = null;
    if (input == null || "".equals(input.trim())) {
      return result;
    }
    List<String> matched = new ArrayList<>();
    Matcher matcher = Pattern.compile(pattern).matcher(input);
    while (matcher.find()) {
      matched.add(matcher.group());
    }
    if (matched.size() > 0) {
      result = String.join("\n", matched);
    }
    return result;
  }

  private void updateUidAndGrouping(String uidFile, String groupFile) throws IOException {
    String uidstr = parseUidAndGrouping(USERNAME_UID_PATTERN, uidFile);
    if (uidstr == null && uidFile != null && !"".equals(uidFile.trim())) {
      uidstr = readFile(uidFile);
    }
    String grouping = parseUidAndGrouping(GROUPNAME_GID_USERNAMES_PATTERN, groupFile);
    if (grouping == null && groupFile != null && !"".equals(groupFile.trim())) {
      grouping = readFile(groupFile);
    }

    lib.jfs_update_uid_grouping(name, uidstr, grouping);
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
    return makeQualified(new Path(homeDirPrefix + "/" + user));
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

  public static Libjfs loadLibrary() {
    initStubLoader();

    LibraryLoader<Libjfs> libjfsLibraryLoader = LibraryLoader.create(Libjfs.class);
    libjfsLibraryLoader.failImmediately();

    String osId = "so";
    String archId = "amd64";
    String resourceFormat = "libjfs-%s.%s.gz";
    String nameFormat = "libjfs-%s.%s.%s";

    File dir = new File("/tmp");
    String os = System.getProperty("os.name");
    String arch = System.getProperty("os.arch");
    if (arch.contains("aarch64")) {
      archId = "arm64";
    }
    if (os.toLowerCase().contains("windows")) {
      osId = "dll";
      dir = new File(System.getProperty("java.io.tmpdir"));
    } else if (os.toLowerCase().contains("mac")) {
      osId = "dylib";
    }

    String resource = String.format(resourceFormat, archId, osId);
    String name = String.format(nameFormat, archId, gitVer, osId);

    File libFile = new File(dir, name);

    InputStream ins;
    long soTime;
    URL location = JuiceFileSystemImpl.class.getProtectionDomain().getCodeSource().getLocation();
    if (location == null) {
      // jar may changed
      return loadExistLib(libjfsLibraryLoader, dir, name, libFile);
    }
    URLConnection con;
    try {
      try {
        con = location.openConnection();
      } catch (FileNotFoundException e) {
        // jar may changed
        return loadExistLib(libjfsLibraryLoader, dir, name, libFile);
      }
      if (location.getProtocol().equals("jar") && (con instanceof JarURLConnection)) {
        LOG.debug("juicefs-hadoop.jar is a nested jar");
        JarURLConnection connection = (JarURLConnection) con;
        JarFile jfsJar = connection.getJarFile();
        ZipEntry entry = jfsJar.getJarEntry(resource);
        soTime = entry.getLastModifiedTime().toMillis();
        ins = jfsJar.getInputStream(entry);
      } else {
        URI locationUri;
        try {
          locationUri = location.toURI();
        } catch (URISyntaxException e) {
          return loadExistLib(libjfsLibraryLoader, dir, name, libFile);
        }
        if (Files.isDirectory(Paths.get(locationUri))) { // for debug: sdk/java/target/classes
          soTime = con.getLastModified();
          ins = JuiceFileSystemImpl.class.getClassLoader().getResourceAsStream(resource);
        } else {
          JarFile jfsJar;
          try {
            jfsJar = new JarFile(locationUri.getPath());
          } catch (FileNotFoundException fne) {
            return loadExistLib(libjfsLibraryLoader, dir, name, libFile);
          }
          ZipEntry entry = jfsJar.getJarEntry(resource);
          soTime = entry.getLastModifiedTime().toMillis();
          ins = jfsJar.getInputStream(entry);
        }
      }

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
            try {
              File org = new File(dir, name);
              Files.move(tmp.toPath(), org.toPath(), StandardCopyOption.ATOMIC_MOVE);
              libFile = org;
            } catch (Exception ade) {
              Files.move(tmp.toPath(), libFile.toPath(), StandardCopyOption.ATOMIC_MOVE);
            }
          }
        }
      }
      ins.close();
    } catch (Exception e) {
      throw new RuntimeException("Init libjfs failed", e);
    }
    return libjfsLibraryLoader.load(libFile.getAbsolutePath());
  }

  private static Libjfs loadExistLib(LibraryLoader<Libjfs> libjfsLibraryLoader, File dir, String name, File libFile) {
    File currentUserLib = new File(dir, System.getProperty("user.name") + "-" + name);
    if (currentUserLib.exists()) {
      return libjfsLibraryLoader.load(currentUserLib.getAbsolutePath());
    } else {
      return libjfsLibraryLoader.load(libFile.getAbsolutePath());
    }
  }

  private void initCache() {
    try {
      List<String> newNodes = discoverNodes(discoverNodesUrl);
      Map<String, String> newCachedHosts = new HashMap<>();
      for (String newNode : newNodes) {
        try {
          newCachedHosts.put(InetAddress.getByName(newNode).getHostAddress(), newNode);
        } catch (UnknownHostException e) {
          LOG.warn("unknown host: " + newNode);
        }
      }

      // if newCachedHosts are not changed, skip
      if (!newCachedHosts.equals(cachedHostsForName.get(name))) {
        List<String> ips = new ArrayList<>(newCachedHosts.keySet());
        LOG.debug("update nodes to: " + String.join(",", ips));
        hashForName.put(name, new ConsistentHash<>(100, ips));
        cachedHostsForName.put(name, newCachedHosts);
      }
    } catch (Throwable e) {
      LOG.warn("failed to discover nodes", e);
    }
  }

  private List<String> discoverNodes(String urls) {
    LOG.debug("fetching nodes from {}", urls);
    Configuration newConf = new Configuration(getConf());
    newConf.setBoolean("juicefs.internal-bg-task", true);
    NodesFetcher fetcher = NodesFetcherBuilder.buildFetcher(urls, name, newConf);
    List<String> fetched = fetcher.fetchNodes(urls);
    if (fetched == null) {
      fetched = new ArrayList<>();
    }
    LOG.debug("fetched nodes: {}", fetched);
    return fetched;
  }

  private BlockLocation makeLocation(long code, long start, long len) {
    long index = (start + len / 2) / blocksize / 4;
    BlockLocation blockLocation;
    String[] ns = new String[cacheReplica];
    String[] hs = new String[cacheReplica];

    Map<String, String> cachedHosts = cachedHostsForName.get(name);
    ConsistentHash<String> hash = hashForName.get(name);
    for (int i = 0; i < cacheReplica; i++) {
      String h = "localhost";
      if (cachedHosts != null && hash != null) {
        h = cachedHosts.getOrDefault(hash.get(code + "-" + (index + i)), "localhost");
      }
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

  private void setStorageId(BlockLocation bl) {
    if (setStorageIds != null) {
      try {
        setStorageIds.invoke(bl, (Object) getStorageIds());
      } catch (IllegalAccessException | InvocationTargetException e) {
        throw new RuntimeException(e);
      }
    }
  }

  @Override
  public BlockLocation[] getFileBlockLocations(FileStatus file, long start, long len) throws IOException {
    if (needCheckPermission() && !checkPathAccess(file.getPath(), FsAction.READ, "getFileBlockLocations")) {
      return superGroupFileSystem.getFileBlockLocations(file, start, len);
    }

    if (isEmpty(discoverNodesUrl) || cacheReplica <= 0) {
      BlockLocation[] bls = super.getFileBlockLocations(file, start, len);
      if (bls != null) {
        for (BlockLocation bl : bls) {
          setStorageId(bl);
        }
      }
      return bls;
    }

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
    BgTaskUtil.putTask(name, "Node fetcher", this::initCache, 10, 10, TimeUnit.MINUTES);
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
    private long fileLen;

    public FileInputStream(Path f, int fd, int size, long fileLen) throws IOException {
      path = f;
      this.fd = fd;
      buf = directBufferPool.getBuffer(size);
      buf.limit(0);
      position = 0;
      this.fileLen = fileLen;
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
      long remaining = fileLen - position + buf.remaining();
      if (remaining > Integer.MAX_VALUE) {
        return Integer.MAX_VALUE;
      }
      return (int)remaining;
    }

    @Override
    public boolean markSupported() {
      return false;
    }

    @Override
    public synchronized int read() throws IOException {
      if (buf == null)
        throw new IOException("stream was closed");
      if (!buf.hasRemaining() && !refill())
        return -1; // EOF
      assert buf.hasRemaining();
      statistics.incrementBytesRead(1);
      return buf.get() & 0xFF;
    }

    @Override
    public synchronized int read(byte[] b, int off, int len) throws IOException {
      if (off < 0 || len < 0 || b.length - off < len)
        throw new IndexOutOfBoundsException();
      return read(ByteBuffer.wrap(b, off, len));
    }

    private boolean refill() throws IOException {
      buf.clear();
      int read = read(position, buf);
      if (read <= 0) {
        buf.limit(0);
        return false; // EOF
      }
      buf.position(0);
      buf.limit(read);
      position += read;
      return true;
    }

    @Override
    public synchronized int read(long pos, byte[] b, int off, int len) throws IOException {
      if (b == null || off < 0 || len < 0 || b.length - off < len) {
        throw new IllegalArgumentException("arguments: " + off + " " + len);
      }
      int got = read(pos, ByteBuffer.wrap(b, off, len));
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
      ByteBuffer srcBuf = buf.duplicate();
      int got = Math.min(b.remaining(), srcBuf.remaining());
      if (got > 0) {
        srcBuf.limit(srcBuf.position() + got);
        b.put(srcBuf);
        buf.position(srcBuf.position());
        statistics.incrementBytesRead(got);
      }
      int more = read(position, b);
      if (more <= 0)
        return got > 0 ? got : -1;
      position += more;
      statistics.incrementBytesRead(more);
      buf.position(0);
      buf.limit(0);
      return got + more;
    }

    private synchronized int read(long pos, ByteBuffer b) throws IOException {
      if (pos < 0)
        throw new EOFException("position is negative");
      if (!b.hasRemaining())
        return 0;
      int got;
      int startPos = b.position();
      got = lib.jfs_pread(Thread.currentThread().getId(), fd, b, b.remaining(), pos);
      if (got == EINVAL)
        throw new IOException("stream was closed");
      if (got < 0)
        throw error(got, path);
      if (got == 0)
        return -1;
      b.position(startPos + got);
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

    public synchronized void skipNBytes(long n) throws IOException {
      if (buf == null) {
        throw new IOException("stream was closed");
      }

      if (n <= 0) {
        return;
      }

      long np = position + n;
      if (np > fileLen) {
        throw new EOFException(String.format("Unable to skip %s bytes (position=%s, fileSize=%s): %s", n, position, fileLen, np));
      }
      position = np;
    }
    @Override
    public synchronized long skip(long n) throws IOException {
      if (n < 0)
        return -1;
      if (buf == null)
        throw new IOException("stream was closed");
      long pos = getPos();
      if (pos + n > fileLen) {
        n = fileLen - pos;
      }
      seek(pos + n);
      return n;
    }

    @Override
    public synchronized void close() throws IOException {
      if (buf == null) {
        return; // already closed
      }
      directBufferPool.returnBuffer(buf);
      buf = null;
      int r = lib.jfs_close(Thread.currentThread().getId(), fd);
      fd = 0;
      if (r < 0)
        throw error(r, path);
    }
  }

  @Override
  public FSDataInputStream open(Path f, int bufferSize) throws IOException {
    if (needCheckPermission() && !checkPathAccess(f, FsAction.READ, "open")) {
      return superGroupFileSystem.open(f, bufferSize);
    }
    statistics.incrementReadOps(1);
    ByteBuffer fileLen = ByteBuffer.allocate(8);
    fileLen.order(ByteOrder.nativeOrder());
    int fd = lib.jfs_open(Thread.currentThread().getId(), handle, normalizePath(f), fileLen, MODE_MASK_R);
    if (fd < 0) {
      throw error(fd, f);
    }
    long len = fileLen.getLong();
    return new FSDataInputStream(new FileInputStream(f, fd, checkBufferSize(bufferSize), len));
  }

  @Override
  public void access(Path path, FsAction mode) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, mode, "access")) {
      superGroupFileSystem.access(path, mode);
      return;
    }
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
      int done = lib.jfs_write(Thread.currentThread().getId(), fd, ByteBuffer.wrap(b, off, len), len);
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
      int done = lib.jfs_write(Thread.currentThread().getId(), fd, ByteBuffer.wrap(new byte[]{(byte) b}), 1);
      if (done == EINVAL)
        throw new IOException("stream was closed");
      if (done < 0)
        throw error(done, path);
      if (done < 1)
        throw new IOException("write");
    }
  }

  static class BufferedFSOutputStream extends BufferedOutputStream implements Syncable {
    private String hflushMethod;
    private boolean closed;

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
    public synchronized void write(int b) throws IOException {
      if (closed) {
        throw new IOException("stream was closed");
      }
      super.write(b);
    }

    @Override
    public synchronized void write(byte[] b, int off, int len) throws IOException {
      if (closed) {
        throw new IOException("stream was closed");
      }
      super.write(b, off, len);
    }

    @Override
    public synchronized void flush() throws IOException {
      if (closed) {
        throw new IOException("stream was closed");
      }
      super.flush();
    }

    @Override
    public synchronized void hflush() throws IOException {
      if (closed) {
        throw new IOException("stream was closed");
      }
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
    public synchronized void hsync() throws IOException {
      if (closed) {
        throw new IOException("stream was closed");
      }
      flush();
      ((FSOutputStream) out).fsync();
    }

    @Override
    public synchronized void close() throws IOException {
      if (closed) {
        return;
      }
      super.close();
      closed = true;
    }

    public OutputStream getOutputStream() {
      return out;
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
    if (needCheckPermission() && !checkPathAccess(f, FsAction.WRITE, "append")) {
      return superGroupFileSystem.append(f, bufferSize, progress);
    }
    statistics.incrementWriteOps(1);
    int fd = lib.jfs_open(Thread.currentThread().getId(), handle, normalizePath(f), null, MODE_MASK_W);
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
    if (needCheckPermission() && !checkAncestorAccess(f, FsAction.WRITE, "create")) {
      if (!overwrite || !superGroupFileSystem.exists(f)) {
        return superGroupFileSystem.create(f, permission, overwrite, bufferSize, replication, blockSize, progress);
      } else if (!checkPathAccess(f, FsAction.WRITE, "create")) {
        return superGroupFileSystem.create(f, permission, overwrite, bufferSize, replication, blockSize, progress);
      }
    }
    statistics.incrementWriteOps(1);
    while (true) {
      int fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort(), uMask.toShort());
      if (fd == ENOENT) {
        Path parent = makeQualified(f).getParent();
        try {
          mkdirs(parent, FsPermission.getDirDefault());
        } catch (FileAlreadyExistsException e) {
        }
        continue;
      }
      if (fd == EEXIST) {
        if (!overwrite || isDirectory(f)) {
          throw new FileAlreadyExistsException("Path already exists: " + f);
        }
        delete(f, false);
        continue;
      }
      if (fd < 0) {
        throw error(fd, makeQualified(f).getParent());
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
    if (needCheckPermission() && !checkAncestorAccess(f, FsAction.WRITE, "createNonRecursive")) {
      if (!flag.contains(CreateFlag.OVERWRITE) || !superGroupFileSystem.exists(f)) {
        return superGroupFileSystem.createNonRecursive(f, permission, flag, bufferSize, replication, blockSize, progress);
      } else if (!checkPathAccess(f, FsAction.WRITE, "createNonRecursive")) {
        return superGroupFileSystem.createNonRecursive(f, permission, flag, bufferSize, replication, blockSize, progress);
      }
    }
    statistics.incrementWriteOps(1);
    int fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort(), uMask.toShort());
    while (fd == EEXIST) {
      if (!flag.contains(CreateFlag.OVERWRITE) || isDirectory(f)) {
        throw new FileAlreadyExistsException("File already exists: " + f);
      }
      delete(f, false);
      fd = lib.jfs_create(Thread.currentThread().getId(), handle, normalizePath(f), permission.toShort(), uMask.toShort());
    }
    if (fd < 0) {
      throw error(fd, makeQualified(f).getParent());
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
    if (needCheckPermission() && !checkPathAccess(f, FsAction.READ, "getFileChecksum")) {
      return superGroupFileSystem.getFileChecksum(f, length);
    }
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

    DataOutputBuffer checksumBuf = new DataOutputBuffer();
    DataOutputBuffer crcBuf = new DataOutputBuffer();
    byte[] buf = new byte[bytesPerCrc];
    FSDataInputStream in = open(f, 1 << 20);
    boolean eof = false;
    long got = 0;
    while (got < length && !eof) {
      for (int i = 0; i < blocksize / bytesPerCrc && got < length; i++) {
        int n;
        if (length < bytesPerCrc) {
          n = in.read(buf, 0, (int) length);
        } else {
          n = in.read(buf);
        }
        if (n <= 0) {
          eof = true;
          break;
        } else {
          summer.update(buf, 0, n);
          summer.writeValue(crcBuf, true);
          got += n;
        }
      }
      if (crcBuf.getLength() > 0) {
        MD5Hash blockMd5 = MD5Hash.digest(crcBuf.getData(), 0, crcBuf.getLength());
        blockMd5.write(checksumBuf);
        crcBuf.reset();
      }
    }
    in.close();
    if (checksumBuf.getLength() == 0) { // empty file
      return new MD5MD5CRC32GzipFileChecksum(0, 0, MD5Hash.digest(new byte[32]));
    }
    MD5Hash md5 = MD5Hash.digest(checksumBuf.getData());
    long crcPerBlock = 0;
    if (got > blocksize) { // more than one block
      crcPerBlock = blocksize / bytesPerCrc;
    }
    if (ctype == DataChecksum.Type.CRC32C) {
      return new MD5MD5CRC32CastagnoliFileChecksum(bytesPerCrc, crcPerBlock, md5);
    } else {
      return new MD5MD5CRC32GzipFileChecksum(bytesPerCrc, crcPerBlock, md5);
    }
  }

  @Override
  public void concat(final Path dst, final Path[] srcs) throws IOException {
    if (needCheckPermission()) {
      access(dst.getParent(), FsAction.WRITE);
      access(dst, FsAction.WRITE);
      for (Path src : srcs) {
        access(src, FsAction.READ);
      }
      superGroupFileSystem.concat(dst, srcs);
      return;
    }
    statistics.incrementWriteOps(1);
    if (srcs.length == 0) {
      throw new IllegalArgumentException("No sources given");
    }
    Path dp = makeQualified(dst).getParent();
    for (Path src : srcs) {
      if (!makeQualified(src).getParent().equals(dp)) {
        throw new HadoopIllegalArgumentException("Source file " + normalizePath(src)
                + " is not in the same directory with the target "
                + normalizePath(dst));
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
      if (r == ENOENT) {
        if (!exists(dst)) {
          throw error(r, dst);
        } else {
          throw new FileNotFoundException("one of srcs is missing");
        }
      }
      throw error(r, dst);
    }
  }

  @Override
  public boolean rename(Path src, Path dst) throws IOException {
    if (needCheckPermission()) {
      if (!superGroupFileSystem.exists(src)) {
        return false;
      }
      access(src.getParent(), FsAction.WRITE);
      Path dstAncestor = rangerPermissionChecker.getAncestor(dst).getPath();
      access(dstAncestor, FsAction.WRITE);
      return superGroupFileSystem.rename(src, dst);
    }
    statistics.incrementWriteOps(1);
    String srcStr = makeQualified(src).toUri().getPath();
    String dstStr = makeQualified(dst).toUri().getPath();
    if (src.equals(dst)) {
      FileStatus st = getFileStatus(src);
      return st.isFile();
    }
    if (dstStr.startsWith(srcStr) && (dstStr.charAt(srcStr.length()) == Path.SEPARATOR_CHAR)) {
      return false;
    }
    int r = lib.jfs_rename(Thread.currentThread().getId(), handle, normalizePath(src), normalizePath(dst));
    if (r == EEXIST) {
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
    if (r == EACCESS) {
      this.access(makeQualified(src).getParent(), FsAction.WRITE.or(FsAction.EXECUTE));
      this.access(makeQualified(dst).getParent(), FsAction.WRITE.or(FsAction.EXECUTE));
    }
    if (r < 0)
      throw error(r, src);
    return true;
  }

  @Override
  public boolean truncate(Path f, long newLength) throws IOException {
    if (needCheckPermission() && !checkPathAccess(f, FsAction.WRITE, "truncate")) {
      return superGroupFileSystem.truncate(f, newLength);
    }
    int r = lib.jfs_truncate(Thread.currentThread().getId(), handle, normalizePath(f), newLength);
    if (r < 0)
      throw error(r, f);
    return true;
  }

  private boolean rmr(Path p) throws IOException {
    int r = lib.jfs_rmr(Thread.currentThread().getId(), handle, normalizePath(p));
    if (r == ENOENT) {
      return false;
    }
    if (r < 0) {
      throw error(r, p);
    }
    return true;
  }

  @Override
  public boolean delete(Path p, boolean recursive) throws IOException {
    if (needCheckPermission()) {
      try {
        if (!checkParentPathAccess(p, FsAction.WRITE_EXECUTE, "delete")) {
          return superGroupFileSystem.delete(p, recursive);
        }
      } catch (Exception e) {
        if (!checkPathAccess(p, FsAction.WRITE_EXECUTE, "delete")) {
          return superGroupFileSystem.delete(p, recursive);
        }
      }
    }
    statistics.incrementWriteOps(1);
    if (recursive)
      return rmr(p);
    int r = lib.jfs_delete(Thread.currentThread().getId(), handle, normalizePath(p));
    if (r == ENOENT) {
      return false;
    }
    if (r < 0) {
      throw error(r, p);
    }
    return true;
  }

  @Override
  public ContentSummary getContentSummary(Path f) throws IOException {
    if (needCheckPermission() && !checkPathAccess(f, FsAction.READ_EXECUTE, "getContentSummary")) {
      return superGroupFileSystem.getContentSummary(f);
    }
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
    boolean hasAcl = (mode >> 18 & 1) == 1;
    FsPermission perm = new FsPermission((short) ((mode & 0777) | (stickybit << 9)));
    perm = new FsPermissionExtension(perm, hasAcl, false);
    long length = buf.getLongLong(4);
    long mtime = buf.getLongLong(12);
    long atime = buf.getLongLong(20);
    String user = buf.getString(28);
    String group = buf.getString(28 + user.length() + 1);
    assert (30 + user.length() + group.length() == size);

    if (fileStatusConstructor == null) {
      return new FileStatus(length, isdir, 1, blocksize, mtime, atime, perm, user, group, p);
    } else {
      try {
        return fileStatusConstructor.newInstance(length, isdir, 1, blocksize, mtime, atime, perm, user, group, null, p, hasAcl, false, false);
      } catch (Exception e) {
        throw new IOException("construct fileStatus failed", e);
      }
    }
  }

  @Override
  public FileStatus[] listStatus(Path f) throws FileNotFoundException, IOException {
    if (needCheckPermission() && !checkPathAccess(f, FsAction.READ_EXECUTE, "listStatus")) {
      return superGroupFileSystem.listStatus(f);
    }
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
        Path p = makeQualified(new Path(f, new String(name)));
        FileStatus st = newFileStatus(p, buf.slice(offset + 1), size, false);
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
    if (needCheckPermission() && !checkAncestorAccess(f, FsAction.WRITE, "mkdirs")) {
      return superGroupFileSystem.mkdirs(f, permission);
    }
    statistics.incrementWriteOps(1);
    if (f == null) {
      throw new IllegalArgumentException("mkdirs path arg is null");
    }
    String path = normalizePath(f);
    if ("/".equals(path))
      return true;
    int r = lib.jfs_mkdir(Thread.currentThread().getId(), handle, path, permission.toShort(), uMask.toShort());
    if (r == 0 || r == EEXIST && !isFile(f)) {
      return true;
    } else if (r == ENOENT) {
      Path parent = makeQualified(f).getParent();
      if (parent != null) {
        return mkdirs(parent, permission) && mkdirs(f, permission);
      }
    }
    throw error(r, makeQualified(f).getParent());
  }

  @Override
  public FileStatus getFileStatus(Path f) throws IOException {
    if (needCheckPermission() && !checkParentPathAccess(f, FsAction.EXECUTE, "getFileStatus")) {
      return superGroupFileSystem.getFileStatus(f);
    }
    statistics.incrementReadOps(1);
    try {
      return getFileStatusInternal(f, true);
    } catch (ParentNotDirectoryException e) {
      throw new FileNotFoundException(f.toString());
    }
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
  public FsStatus getStatus(Path p) throws IOException {
    if (needCheckPermission() && !checkParentPathAccess(p, FsAction.EXECUTE, "getStatus")) {
      return superGroupFileSystem.getStatus(p);
    }
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
    if (needCheckPermission() && !checkOwner(p, "setPermission")) {
      superGroupFileSystem.setPermission(p, permission);
      return;
    }
    statistics.incrementWriteOps(1);
    int r = lib.jfs_chmod(Thread.currentThread().getId(), handle, normalizePath(p), permission.toShort());
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void setOwner(Path p, String username, String groupname) throws IOException {
    if (needCheckPermission()) {
      if (username == null) {
        throw new AccessControlException(
            "User can not be null");
      }
      if (!superuser.equals(username)) {
        throw new AccessControlException(
            "Only SuperUser can do setOwner Action, the current user is " + username);
      }
      superGroupFileSystem.setOwner(p, username, groupname);
      return;
    }
    statistics.incrementWriteOps(1);
    int r = lib.jfs_setOwner(Thread.currentThread().getId(), handle, normalizePath(p), username, groupname);
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void setTimes(Path p, long mtime, long atime) throws IOException {
    if (needCheckPermission() && !checkPathAccess(p, FsAction.WRITE, "setTimes")) {
      superGroupFileSystem.setTimes(p, mtime, atime);
      return;
    }
    statistics.incrementWriteOps(1);
    int r = lib.jfs_utime(Thread.currentThread().getId(), handle, normalizePath(p), mtime >= 0 ? mtime : -1,
        atime >= 0 ? atime : -1);
    if (r != 0)
      throw error(r, p);
  }

  @Override
  public void close() throws IOException {
    super.close();
    RangerPermissionChecker.release(name, handle);
    BgTaskUtil.unregister(name, handle, () -> {
      cachedHostsForName.clear();
      hashForName.clear();
      lastFileStatus.clear();
    });
    LOG.debug("close {}({})", name, handle);
    lib.jfs_term(Thread.currentThread().getId(), handle);
    if (metricsEnable) {
      JuiceFSInstrumentation.close();
    }
  }

  @Override
  public void setXAttr(Path path, String name, byte[] value, EnumSet<XAttrSetFlag> flag) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, FsAction.WRITE, "setXAttr")) {
      superGroupFileSystem.setXAttr(path, name, value, flag);
      return;
    }
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

  @Override
  public byte[] getXAttr(Path path, String name) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, FsAction.READ, "getXAttr")) {
      return superGroupFileSystem.getXAttr(path, name);
    }
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

  @Override
  public Map<String, byte[]> getXAttrs(Path path) throws IOException {
    return getXAttrs(path, listXAttrs(path));
  }

  @Override
  public Map<String, byte[]> getXAttrs(Path path, List<String> names) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, FsAction.READ, "getXAttrs")) {
      return superGroupFileSystem.getXAttrs(path, names);
    }
    Map<String, byte[]> result = new HashMap<String, byte[]>();
    for (String n : names) {
      byte[] value = getXAttr(path, n);
      if (value != null) {
        result.put(n, value);
      }
    }
    return result;
  }

  @Override
  public List<String> listXAttrs(Path path) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, FsAction.READ, "listXAttrs")) {
      return superGroupFileSystem.listXAttrs(path);
    }
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

  @Override
  public void removeXAttr(Path path, String name) throws IOException {
    if (needCheckPermission() && !checkPathAccess(path, FsAction.WRITE, "removeXAttr")) {
      superGroupFileSystem.removeXAttr(path, name);
      return;
    }
    int r = lib.jfs_removeXattr(Thread.currentThread().getId(), handle, normalizePath(path), name);
    if (r == ENOATTR || r == ENODATA) {
      throw new IOException("No matching attributes found for remove operation");
    }
    if (r < 0)
      throw error(r, path);
  }

  @Override
  public void modifyAclEntries(Path path, List<AclEntry> aclSpec) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "modifyAclEntries")) {
      superGroupFileSystem.modifyAclEntries(path, aclSpec);
      return;
    }
    List<AclEntry> existingEntries = getAllAclEntries(path);
    List<AclEntry> newAcl = AclTransformation.mergeAclEntries(existingEntries, aclSpec);
    setAclInternal(path, newAcl);
  }

  @Override
  public void removeAclEntries(Path path, List<AclEntry> aclSpec) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "removeAclEntries")) {
      superGroupFileSystem.removeAclEntries(path, aclSpec);
      return;
    }
    List<AclEntry> existingEntries = getAllAclEntries(path);
    List<AclEntry> newAcl = AclTransformation.filterAclEntriesByAclSpec(existingEntries, aclSpec);
    setAclInternal(path, newAcl);
  }

  @Override
  public void setAcl(Path path, List<AclEntry> aclSpec) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "setAcl")) {
      superGroupFileSystem.setAcl(path, aclSpec);
      return;
    }
    List<AclEntry> existingEntries = getAllAclEntries(path);
    List<AclEntry> newAcl = AclTransformation.replaceAclEntries(existingEntries, aclSpec);
    setAclInternal(path, newAcl);
  }

  private void setAclInternal(Path path, List<AclEntry> aclSpec) throws IOException {
    List<AclEntry> aclEntries = AclTransformation.buildAndValidateAcl(Lists.newArrayList(aclSpec));
    ScopedAclEntries scoped = new ScopedAclEntries(aclEntries);
    setAclInternal(path, AclEntryScope.ACCESS, scoped.getAccessEntries());
    setAclInternal(path, AclEntryScope.DEFAULT, scoped.getDefaultEntries());
  }

  private void removeAclInternal(Path path, AclEntryScope scope) throws IOException {
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 6 * 2);
    buf.putShort(0, (short) -1);
    buf.putShort(2, (short) -1);
    buf.putShort(4, (short) -1);
    buf.putShort(6, (short) -1);
    buf.putShort(8, (short) 0);
    buf.putShort(10, (short) 0);
    int r = lib.jfs_setfacl(Thread.currentThread().getId(), handle, normalizePath(path), scope.ordinal() + 1, buf,
        6 * 2);
    if (r == ENOATTR || r == ENODATA)
      return;
    if (r < 0)
      throw error(r, path);
  }

  @Override
  public void removeDefaultAcl(Path path) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "removeDefaultAcl")) {
      superGroupFileSystem.removeDefaultAcl(path);
      return;
    }
    removeAclInternal(path, AclEntryScope.DEFAULT);
  }

  @Override
  public void removeAcl(Path path) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "removeAcl")) {
      superGroupFileSystem.removeAcl(path);
      return;
    }
    removeAclInternal(path, AclEntryScope.ACCESS);
    removeAclInternal(path, AclEntryScope.DEFAULT);
  }

  private void setAclInternal(Path path, AclEntryScope scope, List<AclEntry> aclSpec) throws IOException {
    if (aclSpec.size() == 0)
      return;
    short userperm = -1, groupperm = -1, otherperm = -1, maskperm = -1;
    short namedusers = 0, namedgroups = 0;
    int namedaclsize = 0;
    for (AclEntry e : aclSpec) {
      if (e.getName() != null) {
        if (e.getType() == AclEntryType.USER) {
          namedusers++;
        } else {
          namedgroups++;
        }
        namedaclsize += e.getName().getBytes("utf8").length + 2;
      } else {
        short perm = (short) e.getPermission().ordinal();
        switch (e.getType()) {
          case USER:
            userperm = perm;
            break;
          case GROUP:
            groupperm = perm;
            break;
          case OTHER:
            otherperm = perm;
            break;
          case MASK:
            maskperm = perm;
            break;
        }
      }
    }
    Pointer buf = Memory.allocate(Runtime.getRuntime(lib), 12 + namedaclsize);
    buf.putShort(0, userperm);
    buf.putShort(2, groupperm);
    buf.putShort(4, otherperm);
    buf.putShort(6, maskperm);
    buf.putShort(8, namedusers);
    buf.putShort(10, namedgroups);
    int off = 12;
    for (AclEntry e : aclSpec) {
      String name = e.getName();
      if (name != null && e.getType() == AclEntryType.USER) {
        byte[] nb = name.getBytes("utf8");
        buf.putByte(off, (byte) nb.length);
        buf.put(off + 1, nb, 0, nb.length);
        off += 1 + nb.length;
        buf.putByte(off, (byte) e.getPermission().ordinal());
        off += 1;
      }
    }
    for (AclEntry e : aclSpec) {
      String name = e.getName();
      if (name != null && e.getType() == AclEntryType.GROUP) {
        byte[] nb = name.getBytes("utf8");
        buf.putByte(off, (byte) nb.length);
        buf.put(off + 1, nb, 0, nb.length);
        off += 1 + nb.length;
        buf.putByte(off, (byte) e.getPermission().ordinal());
        off += 1;
      }
    }
    int r = lib.jfs_setfacl(Thread.currentThread().getId(), handle, normalizePath(path), scope.ordinal() + 1, buf,
        12 + namedaclsize);
    if (r == ENOTSUP) {
      throw new IOException("Invalid ACL: only directories may have a default ACL");
    }
    if (r < 0)
      throw error(r, path);
  }

  private List<AclEntry> getAclEntries(Path path, AclEntryScope scope) throws IOException {
    int bufsize = 1024;
    int r;
    Pointer buf;
    do {
      bufsize *= 2;
      buf = Memory.allocate(Runtime.getRuntime(lib), bufsize);
      r = lib.jfs_getfacl(Thread.currentThread().getId(), handle, normalizePath(path), scope.ordinal() + 1, buf,
          bufsize);
    } while (r == -100);
    if (r == ENOATTR || r == ENODATA) {
      return Lists.newArrayList();
    }
    if (r < 0)
      throw error(r, path);

    int off = 0;
    short userperm = buf.getShort(0);
    short groupperm = buf.getShort(2);
    short otherperm = buf.getShort(4);
    short maskperm = buf.getShort(6);
    short namedusers = buf.getShort(8);
    short namedgroups = buf.getShort(10);
    off += 12;

    List<AclEntry> entries = new ArrayList<>();
    AclEntry.Builder builder = new AclEntry.Builder().setScope(scope);
    if (userperm != -1) {
      entries.add(builder.setType(AclEntryType.USER).setPermission(FsAction.values()[userperm]).build());
    }
    if (groupperm != -1) {
      entries.add(builder.setType(AclEntryType.GROUP).setPermission(FsAction.values()[groupperm]).build());
    }
    if (otherperm != -1) {
      entries.add(builder.setType(AclEntryType.OTHER).setPermission(FsAction.values()[otherperm]).build());
    }
    if (maskperm != -1) {
      entries.add(builder.setType(AclEntryType.MASK).setPermission(FsAction.values()[maskperm]).build());
    }

    for (int i = 0; i < namedusers + namedgroups; i++) {
      String name = buf.getString(off);
      off += name.length() + 1;
      short perm = buf.getShort(off);
      off += 2;
      entries.add(builder.setType(i < namedusers ? AclEntryType.USER : AclEntryType.GROUP).setName(name)
          .setPermission(FsAction.values()[perm]).build());
    }
    Collections.sort(entries, AclTransformation.ACL_ENTRY_COMPARATOR);
    return entries;
  }

  /**
   * include acl entries from permission
   */
  private List<AclEntry> getAllAclEntries(Path path) throws IOException {
    List<AclEntry> entries = getAclEntries(path, AclEntryScope.ACCESS);
    if (entries.size() == 0) {
      FsPermission perm = getFileStatus(path).getPermission();
      entries = AclUtil.getAclFromPermAndEntries(perm, entries);
    }
    entries.addAll(getAclEntries(path, AclEntryScope.DEFAULT));
    return entries;
  }

  /**
   * exclude acl entries from permission
   */
  private List<AclEntry> getAclEntries(Path path) throws IOException {
    List<AclEntry> res = new ArrayList<>();
    List<AclEntry> accessEntries = getAclEntries(path, AclEntryScope.ACCESS);
    // minimal 3 acls for ugo
    if (accessEntries.size() != 0 && accessEntries.size() != 3) {
      res.addAll(accessEntries.subList(1, accessEntries.size() - 2));
    }
    res.addAll(getAclEntries(path, AclEntryScope.DEFAULT));
    return res;
  }

  @Override
  public AclStatus getAclStatus(Path path) throws IOException {
    if (needCheckPermission() && !checkOwner(path, "getAclStatus")) {
      return superGroupFileSystem.getAclStatus(path);
    }
    FileStatus st = getFileStatus(path);
    List<AclEntry> entries = getAclEntries(path);
    AclStatus.Builder builder = new AclStatus.Builder().owner(st.getOwner()).group(st.getGroup())
        .stickyBit(st.getPermission().getStickyBit()).addEntries(entries);
    try {
      Class<AclStatus.Builder> ab = AclStatus.Builder.class;
      Method abm = ab.getDeclaredMethod("setPermission", FsPermission.class);
      abm.setAccessible(true);
      abm.invoke(builder, st.getPermission());
    } catch (NoSuchMethodException | IllegalAccessException | InvocationTargetException ignored) {
    }
    return builder.build();
  }
}
