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
import io.juicefs.utils.AclTransformation;
import junit.framework.TestCase;
import org.apache.commons.io.IOUtils;
import org.apache.flink.runtime.fs.hdfs.HadoopRecoverableWriter;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.*;
import org.apache.hadoop.io.MD5Hash;
import org.apache.hadoop.security.AccessControlException;
import org.apache.hadoop.security.UserGroupInformation;

import java.io.FileNotFoundException;
import java.io.IOException;
import java.io.OutputStream;
import java.lang.reflect.Method;
import java.net.InetAddress;
import java.net.URI;
import java.nio.ByteBuffer;
import java.security.PrivilegedExceptionAction;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;

import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_CHECKPOINT_INTERVAL_KEY;
import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_INTERVAL_KEY;
import static org.apache.hadoop.fs.permission.AclEntryScope.ACCESS;
import static org.apache.hadoop.fs.permission.AclEntryScope.DEFAULT;
import static org.apache.hadoop.fs.permission.AclEntryType.*;
import static org.apache.hadoop.fs.permission.FsAction.*;
import static org.junit.Assert.assertArrayEquals;

public class JuiceFileSystemTest extends TestCase {
  FsShell shell;
  FileSystem fs;
  Configuration cfg;

  public void setUp() throws Exception {
    cfg = new Configuration();
    cfg.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    cfg.set(FS_TRASH_INTERVAL_KEY, "6");
    cfg.set(FS_TRASH_CHECKPOINT_INTERVAL_KEY, "2");
    cfg.set("juicefs.access-log", "/tmp/jfs.access.log");
    cfg.set("juicefs.discover-nodes-url", "jfs:///etc/nodes");
    fs = FileSystem.newInstance(cfg);
    fs.delete(new Path("/hello"));
    FSDataOutputStream out = fs.create(new Path("/hello"), true);
    out.writeBytes("hello\n");
    out.close();

    cfg.setQuietMode(false);
    shell = new FsShell(cfg);
  }

  public void tearDown() throws Exception {
    fs.close();
    FileSystem.closeAll();
  }

  public void testFsStatus() throws IOException {
    FsStatus st = fs.getStatus();
    assertTrue("capacity", st.getCapacity() > 0);
    assertTrue("remaining", st.getRemaining() > 0);
  }

  public void testSummary() throws IOException {
    ContentSummary summary = fs.getContentSummary(new Path("/"));
    assertTrue("length", summary.getLength() > 0);
    assertTrue("fileCount", summary.getFileCount() > 0);
    summary = fs.getContentSummary(new Path("/hello"));
    assertEquals(6, summary.getLength());
    assertEquals(1, summary.getFileCount());
    assertEquals(0, summary.getDirectoryCount());
  }

  public void testLongName() throws IOException {
    Path p = new Path(
            "/longname/very_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_long_name");
    fs.mkdirs(p);
    FileStatus[] files = fs.listStatus(new Path("/longname"));
    if (files.length != 1) {
      throw new IOException("expected one file but got " + files.length);
    }
    if (!files[0].getPath().getName().equals(p.getName())) {
      throw new IOException("not equal");
    }
  }

  public void testLocation() throws IOException {
    FileStatus f = new FileStatus(3L << 30, false, 1, 128L << 20, 0, new Path("/hello"));
    BlockLocation[] locations = fs.getFileBlockLocations(f, 128L * 1024 * 1024 - 256, 5L * 64 * 1024 * 1024 - 512L);

    String[] names = locations[0].getNames();
    for (String name : names) {
      assertEquals(name.split(":").length, 2);
    }

    String[] storageIds = locations[0].getStorageIds();
    assertNotNull(storageIds);
    assertEquals(names.length, storageIds.length);

    assertEquals(InetAddress.getLocalHost().getHostName() + ":50010", names[0]);
  }

  public void testReadWrite() throws Exception {
    long l = fs.getFileStatus(new Path("/hello")).getLen();
    assertEquals(6, l);
    byte[] buf = new byte[(int) l];
    FSDataInputStream in = fs.open(new Path("/hello"));
    in.readFully(buf);
    in.close();
    assertEquals("hello\n", new String(buf));
    assertEquals(0, shell.run(new String[]{"-cat", "/hello"}));

    fs.setPermission(new Path("/hello"), new FsPermission((short) 0000));
    UserGroupInformation ugi =
            UserGroupInformation.createUserForTesting("nobody", new String[]{"nogroup"});
    FileSystem fs2 = ugi.doAs(new PrivilegedExceptionAction<FileSystem>() {
      @Override
      public FileSystem run() throws Exception {
        return FileSystem.get(new URI("jfs://dev"), cfg);
      }
    });
    try {
      in = fs2.open(new Path("/hello"));
      assertEquals(in, null);
    } catch (IOException e) {
      fs.setPermission(new Path("/hello"), new FsPermission((short) 0644));
    }
  }

  public void testWrite() throws Exception {
    Path f = new Path("/testWriteFile");
    FSDataOutputStream fou = fs.create(f);
    byte[] b = "hello world".getBytes();
    OutputStream ou = ((JuiceFileSystemImpl.BufferedFSOutputStream)fou.getWrappedStream()).getOutputStream();
    ou.write(b, 6, 5);
    ou.close();
    FSDataInputStream in = fs.open(f);
    String str = IOUtils.toString(in);
    assertEquals("world", str);
    in.close();

    int fileLen = 1 << 20;
    byte[] contents = new byte[fileLen];
    Random random = new Random();
    random.nextBytes(contents);
    f = new Path("/tmp/writeFile");
    FSDataOutputStream out = fs.create(f);
    int off = 0;
    int len = 256<<10;
    out.write(contents, off, len);
    out.close();

    byte[] readBytes = new byte[len];
    in = fs.open(f);
    in.read(readBytes);
    assertArrayEquals(Arrays.copyOfRange(contents, off, off + len), readBytes);
    in.close();

    out = fs.create(f);
    off = 0;
    len = fileLen;
    for (int i = off; i < len; i++) {
      out.write(contents[i]);
    }
    out.hflush();
    readBytes = new byte[len];
    in = fs.open(f);
    in.read(readBytes);
    assertArrayEquals(Arrays.copyOfRange(contents, off, off + len), readBytes);
    out.close();
    in.close();
  }

  public void testReadSkip() throws Exception {
    Path p = new Path("/test_readskip");
    fs.create(p).close();
    String content = "12345";
    writeFile(fs, p, content);
    FSDataInputStream in = fs.open(p);
    long skip = in.skip(2);
    assertEquals(2, skip);

    byte[] bytes = new byte[content.length() - (int)skip];
    in.readFully(bytes);
    assertEquals("345", new String(bytes));
  }

  public void testReadAfterClose() throws Exception {
    byte[] buf = new byte[6];
    FSDataInputStream in = fs.open(new Path("/hello"));
    in.close();
    try {
      in.read(0, buf, 0, 5);
    } catch (IOException e) {
      if (!e.getMessage().contains("closed")) {
        throw new IOException("message should be closed, but got " + e.getMessage());
      }
    }
    FSDataInputStream in2 = fs.open(new Path("/hello"));
    in.close();  // repeated close should not close other's fd
    in2.read(0, buf, 0, 5);
    in2.close();
  }

  public void testMkdirs() throws Exception {
    assertTrue(fs.mkdirs(new Path("/mkdirs")));
    assertTrue(fs.mkdirs(new Path("/mkdirs/dir")));
    assertTrue(fs.delete(new Path("/mkdirs"), true));
    assertTrue(fs.mkdirs(new Path("/mkdirs/test")));
    for (int i = 0; i < 50; i++) {
      fs.mkdirs(new Path("/mkdirs/d" + i));
    }
    assertEquals(51, fs.listStatus(new Path("/mkdirs/")).length);
    assertTrue(fs.delete(new Path("/mkdirs"), true));
    assertTrue(fs.mkdirs(new Path("parent/dir")));
    assertTrue(fs.exists(new Path(fs.getHomeDirectory(), "parent")));
  }

  public void testCreateWithoutPermission() throws Exception {
    assertTrue(fs.mkdirs(new Path("/noperm")));
    fs.setPermission(new Path("/noperm"), new FsPermission((short) 0555));
    UserGroupInformation ugi =
            UserGroupInformation.createUserForTesting("nobody", new String[]{"nogroup"});
    FileSystem fs2 = ugi.doAs(new PrivilegedExceptionAction<FileSystem>() {
      @Override
      public FileSystem run() throws Exception {
        return FileSystem.get(new URI("jfs://dev"), cfg);
      }
    });
    try {
      fs2.create(new Path("/noperm/a/file"));
      throw new Exception("create should fail");
    } catch (IOException e) {
    }
  }

  public void testCreateNonRecursive() throws Exception {
    Path p = new Path("/NOT_EXIST_DIR");
    p = new Path(p, "file");
    try (FSDataOutputStream ou = fs.createNonRecursive(p, false, 1 << 20, (short) 1, 128 << 20, null);) {
      fail("createNonRecursive in a not exit dir should fail");
    } catch (IOException ignored) {
    }
  }

  public void testTruncate() throws Exception {
    Path p = new Path("/test_truncate");
    fs.create(p).close();
    fs.truncate(p, 1 << 20);
    assertEquals(1 << 20, fs.getFileStatus(p).getLen());
    fs.truncate(p, 1 << 10);
    assertEquals(1 << 10, fs.getFileStatus(p).getLen());
  }

  public void testAccess() throws Exception {
    Path p1 = new Path("/test_access");
    FileSystem newFs = createNewFs(cfg, "user1", new String[]{"group1"});
    newFs.create(p1).close();
    newFs.setPermission(p1, new FsPermission((short) 0444));
    newFs.access(p1, FsAction.READ);
    try {
      newFs.access(p1, FsAction.WRITE);
      fail("The access call should have failed.");
    } catch (AccessControlException e) {
    }

    Path badPath = new Path("/bad/bad");
    try {
      newFs.access(badPath, FsAction.READ);
      fail("The access call should have failed");
    } catch (FileNotFoundException e) {
    }
    newFs.close();
  }

  public void testSetPermission() throws Exception {
    assertEquals(0, shell.run(new String[]{"-chmod", "0777", "/hello"}));
    assertEquals(0777, fs.getFileStatus(new Path("/hello")).getPermission().toShort());
    assertEquals(0, shell.run(new String[]{"-chmod", "0666", "/hello"}));
    assertEquals(0666, fs.getFileStatus(new Path("/hello")).getPermission().toShort());
  }

  public void testSetTimes() throws Exception {
    fs.setTimes(new Path("/hello"), 1000, 2000);
    assertEquals(1000, fs.getFileStatus(new Path("/hello")).getModificationTime());
    // assertEquals(2000, fs.getFileStatus(new Path("/hello")).getAccessTime());

    Path p = new Path("/test-mtime");
    fs.delete(p, true);
    FSDataOutputStream out = fs.create(p);
    Thread.sleep(1000);
    long mtime1 = fs.getFileStatus(p).getModificationTime();
    out.writeBytes("hello\n");
    out.close();
    long mtime2 = fs.getFileStatus(p).getModificationTime();
    if (mtime2 - mtime1 < 1000) {
      throw new IOException("stale mtime");
    }
    Thread.sleep(1000);
    long mtime3 = fs.getFileStatus(p).getModificationTime();
    if (mtime3 != mtime2) {
      throw new IOException("mtime was updated");
    }
  }

  public void testSetOwner() throws Exception {
    fs.create(new Path("/hello"));
    FileStatus parent = fs.getFileStatus(new Path("/"));
    FileStatus st = fs.getFileStatus(new Path("/hello"));
    if (!parent.getGroup().equals(st.getGroup())) {
      throw new Exception(
              "group of new created file should be " + parent.getGroup() + ", but got " + st.getGroup());
    }
    return; // only root can change the owner/group to others
    // fs.setOwner(new Path("/hello"), null, "nogroup");
    // assertEquals("nogroup", fs.getFileStatus(new Path("/hello")).getGroup());
  }

  public void testCloseFileSystem() throws Exception {
    Configuration conf = new Configuration();
    conf.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    for (int i = 0; i < 5; i++) {
      FileSystem fs = FileSystem.get(conf);
      fs.getFileStatus(new Path("/hello"));
      fs.close();
    }
  }

  public void testReadahead() throws Exception {
    FSDataOutputStream out = fs.create(new Path("/hello"), true);
    for (int i = 0; i < 1000000; i++) {
      out.writeBytes("hello\n");
    }
    out.close();

    // simulate reading a parquet file
    int size = 1000000 * 6;
    byte[] buf = new byte[128000];
    FSDataInputStream in = fs.open(new Path("/hello"));
    in.read(size - 8, buf, 0, 8);
    in.read(size - 5000, buf, 0, 3000);
    in.close();
    in = fs.open(new Path("/hello"));
    in.read(size - 8, buf, 0, 8);
    in.read(size - 5000, buf, 0, 3000);
    in.close();
    in = fs.open(new Path("/hello"));
    in.read(2000000, buf, 0, 128000);
    in.close();
  }

  public void testOutputStream() throws Exception {
    FSDataOutputStream out = fs.create(new Path("/haha"));
    if (!(out instanceof Syncable)) {
      throw new RuntimeException("FSDataOutputStream should be syncable");
    }
    if (!(out.getWrappedStream() instanceof Syncable)) {
      throw new RuntimeException("BufferedOutputStream should be syncable");
    }
    out.hflush();
    out.hsync();
  }

  public void testInputStream() throws Exception {
    FSDataInputStream in = fs.open(new Path("/hello"));
    if (!(in instanceof ByteBufferReadable)) {
      throw new RuntimeException("Inputstream should be bytebufferreadable");
    }
    if (!(in.getWrappedStream() instanceof ByteBufferReadable)) {
      throw new RuntimeException("Inputstream should not be bytebufferreadable");
    }

    FSDataOutputStream out = fs.create(new Path("/hello"), true);
    for (int i = 0; i < 1000000; i++) {
      out.writeBytes("hello\n");
    }
    out.close();

    in = fs.open(new Path("/hello"));
    ByteBuffer buf = ByteBuffer.allocateDirect(6 * 1000000);
    buf.put((byte) in.read());
    while (buf.hasRemaining()) {
      int readCount = in.read(buf);
      if (readCount == -1) {
        // this is probably a bug in the ParquetReader. We shouldn't have called
        // readFully with a buffer
        // that has more remaining than the amount of data in the stream.
        throw new IOException("Reached the end of stream. Still have: " + buf.remaining() + " bytes left");
      }
    }

    Path directReadFile = new Path("/direct_file");
    FSDataOutputStream ou = fs.create(directReadFile);
    ou.write("hello world".getBytes());
    ou.close();
    FSDataInputStream dto = fs.open(directReadFile);
    ByteBuffer directBuf = ByteBuffer.allocateDirect(11);
    directBuf.put("hello ".getBytes());
    dto.seek(6);
    dto.read(directBuf);
    byte[] rest = new byte[11];
    directBuf.flip();
    directBuf.get(rest, 0, rest.length);
    assertEquals("hello world", new String(rest));

    /*
     * FSDataOutputStream out = fs.create(new Path("/bigfile"), true); byte[] arr =
     * new byte[1<<20]; for (int i=0; i<1024; i++) { out.write(arr); } out.close();
     *
     * long start = System.currentTimeMillis(); in = fs.open(new Path("/bigfile"));
     * ByteBuffer buf = ByteBuffer.allocateDirect(1<<20); long total=0; while (true)
     * { int n = in.read(buf); total += n; if (n < buf.capacity()) { break; } } long
     * used = System.currentTimeMillis() - start;
     * System.out.printf("ByteBuffer read %d throughput %f MB/s\n", total,
     * total/1024.0/1024.0/used*1000);
     *
     * start = System.currentTimeMillis(); in = fs.open(new Path("/bigfile"));
     * total=0; while (true) { int n = in.read(buf); total += n; if (n <
     * buf.capacity()) { break; } } used = System.currentTimeMillis() - start;
     * System.out.printf("ByteBuffer read %d throughput %f MB/s\n", total,
     * total/1024.0/1024.0/used*1000);
     *
     * start = System.currentTimeMillis(); in = fs.open(new Path("/bigfile"));
     * total=0; while (true) { int n = in.read(arr); total += n; if (n <
     * buf.capacity()) { break; } } used = System.currentTimeMillis() - start;
     * System.out.printf("Array read %d throughput %f MB/s\n", total,
     * total/1024.0/1024.0/used*1000);
     */
  }

  public void testInputStreamSkipNBytes() throws Exception {
    Path f = new Path("/test-skipnbytes");
    try (FSDataOutputStream out = fs.create(f)) {
      out.writeBytes("hello juicefs");
    }
    Class<JuiceFileSystemImpl.FileInputStream> inputStreamClass = JuiceFileSystemImpl.FileInputStream.class;
    Method skipNBytes = inputStreamClass.getMethod("skipNBytes", long.class);
    try (FSDataInputStream in = fs.open(f)) {
      skipNBytes.invoke(in.getWrappedStream(), 6);
      String s = IOUtils.toString(in);
      assertEquals("juicefs", s);
    }
  }

  public void testReadStats() throws IOException {
    FileSystem.Statistics statistics = FileSystem.getStatistics(fs.getScheme(),
            ((FilterFileSystem) fs).getRawFileSystem().getClass());
    statistics.reset();
    Path path = new Path("/hello");
    FSDataOutputStream out = fs.create(path, true);
    for (int i = 0; i < 1 << 20; i++) {
      out.writeBytes("hello\n");
    }
    out.close();
    FSDataInputStream in = fs.open(path);

    int readSize = 512 << 10;

    ByteBuffer buf = ByteBuffer.allocateDirect(readSize);
    while (buf.hasRemaining()) {
      in.read(buf);
    }
    assertEquals(readSize, statistics.getBytesRead());

    in.seek(0);
    buf = ByteBuffer.allocate(readSize);
    while (buf.hasRemaining()) {
      in.read(buf);
    }
    assertEquals(readSize * 2, statistics.getBytesRead());

    in.read(0, new byte[3000], 0, 3000);
    assertEquals(readSize * 2 + 3000, statistics.getBytesRead());

    in.read(3000, new byte[6000], 0, 3000);
    assertEquals(readSize * 2 + 3000 + 3000, statistics.getBytesRead());

    in.read(new byte[3000], 0, 3000);
    assertEquals(readSize * 2 + 3000 + 3000 + 3000, statistics.getBytesRead());

    in.close();
  }

  public void testChecksum() throws IOException {
    Path f = new Path("/empty");
    FSDataOutputStream out = fs.create(f, true);
    out.close();
    FileChecksum sum = fs.getFileChecksum(f);
    assertEquals(new MD5MD5CRC32GzipFileChecksum(0, 0, new MD5Hash("70bc8f4b72a86921468bf8e8441dce51")), sum);

    f = new Path("/small");
    out = fs.create(f, true);
    out.writeBytes("world\n");
    out.close();
    sum = fs.getFileChecksum(f);
    assertEquals(new MD5MD5CRC32CastagnoliFileChecksum(512, 0, new MD5Hash("a74dcf6d5ba98e50ae0182c9d5d886fe")),
            sum);
    sum = fs.getFileChecksum(f, 5);
    assertEquals(new MD5MD5CRC32CastagnoliFileChecksum(512, 0, new MD5Hash("05a157db1cc7549c82ec6f31f63fdb46")),
            sum);

    f = new Path("/medium");
    out = fs.create(f, true);
    byte[] bytes = new byte[(128 << 20) - 1];
    out.write(bytes);
    out.close();
    sum = fs.getFileChecksum(f);
    assertEquals(
            new MD5MD5CRC32CastagnoliFileChecksum(512, 0, new MD5Hash("1cf326bae8274fd824ec69ece3e4082f")),
            sum);

    f = new Path("/big");
    out = fs.create(f, true);
    byte[] zeros = new byte[1024 * 1000];
    for (int i = 0; i < 150; i++) {
      out.write(zeros);
    }
    out.close();
    sum = fs.getFileChecksum(f);
    assertEquals(
            new MD5MD5CRC32CastagnoliFileChecksum(512, 262144, new MD5Hash("7d04ac8132ad64988f7ba4d819cbde62")),
            sum);
  }

  public void testXattr() throws IOException {
    Path p = new Path("/test-xattr");
    fs.delete(p, true);
    fs.create(p);
    assertEquals(null, fs.getXAttr(p, "x1"));
    fs.setXAttr(p, "x1", new byte[1]);
    fs.setXAttr(p, "x2", new byte[2]);
    List<String> names = fs.listXAttrs(p);
    assertEquals(2, names.size());
    Map<String, byte[]> values = fs.getXAttrs(p);
    assertEquals(2, values.size());
    assertEquals(1, values.get("x1").length);
    assertEquals(2, values.get("x2").length);
    fs.removeXAttr(p, "x2");
    names = fs.listXAttrs(p);
    assertEquals(1, names.size());
    assertEquals("x1", names.get(0));

    // stress
    for (int i = 0; i < 100; i++) {
      fs.setXAttr(p, "test" + i, new byte[4096]);
    }
    values = fs.getXAttrs(p);
    assertEquals(101, values.size());
    // xattr should be remove together with file
    fs.delete(p);
    fs.create(p);
    names = fs.listXAttrs(p);
    assertEquals(0, names.size());
  }

  public void testAppend() throws Exception {
    Path f = new Path("/tmp/testappend");
    fs.delete(f);
    FSDataOutputStream out = fs.create(f);
    out.write("hello".getBytes());
    out.close();
    FSDataOutputStream append = fs.append(f);
    assertEquals(5, append.getPos());
  }

  public void testFlinkHadoopRecoverableWriter() throws Exception {
    new HadoopRecoverableWriter(fs);
  }

  public void testConcat() throws Exception {
    Path trg = new Path("/tmp/concat");
    Path src1 = new Path("/tmp/concat1");
    Path src2 = new Path("/tmp/concat2");
    FSDataOutputStream ou = fs.create(trg);
    ou.write("hello".getBytes());
    ou.close();
    FSDataOutputStream sou1 = fs.create(src1);
    sou1.write("hello".getBytes());
    sou1.close();
    FSDataOutputStream sou2 = fs.create(src2);
    sou2.write("hello".getBytes());
    sou2.close();
    fs.concat(trg, new Path[]{src1, src2});
    FSDataInputStream in = fs.open(trg);
    assertEquals("hellohellohello", IOUtils.toString(in));
    in.close();
    // src should be deleted after concat
    assertFalse(fs.exists(src1));
    assertFalse(fs.exists(src2));

    Path emptyFile = new Path("/tmp/concat_empty_file");
    Path src = new Path("/tmp/concat_empty_file_src");
    FSDataOutputStream srcOu = fs.create(src);
    srcOu.write("hello".getBytes());
    srcOu.close();
    fs.create(emptyFile).close();
    fs.concat(emptyFile, new Path[]{src});
    in = fs.open(emptyFile);
    assertEquals("hello", IOUtils.toString(in));
    in.close();
  }

  public void testList() throws Exception {
    Path p = new Path("/listsort");
    String[] org = new String[]{
            "/listsort/p4",
            "/listsort/p2",
            "/listsort/p1",
            "/listsort/p3"
    };
    fs.mkdirs(p);
    for (String path : org) {
      fs.mkdirs(new Path(path));
    }
    FileStatus[] fss = fs.listStatus(p);
    String[] res = new String[fss.length];
    for (int i = 0; i < fss.length; i++) {
      res[i] = fss[i].getPath().toUri().getPath();
    }
    Arrays.sort(org);
    assertArrayEquals(org, res);
  }

  private void writeFile(FileSystem fs, Path p, String content) throws IOException {
    FSDataOutputStream ou = fs.create(p);
    ou.write(content.getBytes());
    ou.close();
  }

  public FileSystem createNewFs(Configuration conf, String user, String[] group) throws IOException, InterruptedException {
    if (user != null && group != null) {
      UserGroupInformation root = UserGroupInformation.createUserForTesting(user, group);
      return root.doAs((PrivilegedExceptionAction<FileSystem>) () -> FileSystem.newInstance(FileSystem.getDefaultUri(conf), conf));
    }
    return FileSystem.newInstance(FileSystem.getDefaultUri(conf), conf);
  }

  public void testUsersAndGroups() throws Exception {
    Path users1 = new Path("/tmp/users1");
    Path groups1 = new Path("/tmp/groups1");
    Path users2 = new Path("/tmp/users2");
    Path groups2 = new Path("/tmp/groups2");

    writeFile(fs, users1, "user1:2001\n");
    writeFile(fs, groups1, "group1:3001:user1\n");
    writeFile(fs, users2, "user2:2001\n");
    writeFile(fs, groups2, "group2:3001:user2\n");
    fs.close();

    Configuration conf = new Configuration(cfg);
    conf.set("juicefs.users", users1.toUri().getPath());
    conf.set("juicefs.groups", groups1.toUri().getPath());
    conf.set("juicefs.superuser", UserGroupInformation.getCurrentUser().getShortUserName());

    FileSystem newFs = createNewFs(conf, null, null);
    Path p = new Path("/test_user_group_file");
    newFs.create(p).close();
    newFs.setOwner(p, "user1", "group1");
    newFs.close();

    conf.set("juicefs.users", users2.toUri().getPath());
    conf.set("juicefs.groups", groups2.toUri().getPath());
    newFs = createNewFs(conf, null, null);
    FileStatus fileStatus = newFs.getFileStatus(p);
    assertEquals("user2", fileStatus.getOwner());
    assertEquals("group2", fileStatus.getGroup());
    newFs.close();
  }

  public void testGroupPerm() throws Exception {
    Path testPath = new Path("/test_group_perm");

    Configuration conf = new Configuration(cfg);
    conf.set("juicefs.supergroup", "hadoop");
    conf.set("juicefs.superuser", "hadoop");
    FileSystem uer1Fs = createNewFs(conf, "user1", new String[]{"hadoop"});
    uer1Fs.delete(testPath, true);
    uer1Fs.mkdirs(testPath);
    uer1Fs.setPermission(testPath, FsPermission.createImmutable((short) 0775));
    uer1Fs.close();

    FileSystem uer2Fs = createNewFs(conf, "user2", new String[]{"hadoop"});
    Path f = new Path(testPath, "test_file");
    uer2Fs.create(f).close();
    FileStatus fileStatus = uer2Fs.getFileStatus(f);
    assertEquals("user2", fileStatus.getOwner());
    uer2Fs.close();
  }

  public void testUmask() throws Exception {
    Configuration conf = new Configuration(cfg);
    conf.set("juicefs.umask", "077");
    UserGroupInformation currentUser = UserGroupInformation.getCurrentUser();
    FileSystem newFs = createNewFs(conf, currentUser.getShortUserName(), currentUser.getGroupNames());
    newFs.delete(new Path("/test_umask"), true);
    newFs.mkdirs(new Path("/test_umask/dir"));
    newFs.create(new Path("/test_umask/dir/f")).close();
    assertEquals(FsPermission.createImmutable((short) 0700), newFs.getFileStatus(new Path("/test_umask")).getPermission());
    assertEquals(FsPermission.createImmutable((short) 0700), newFs.getFileStatus(new Path("/test_umask/dir")).getPermission());
    assertEquals(FsPermission.createImmutable((short) 0600), newFs.getFileStatus(new Path("/test_umask/dir/f")).getPermission());
    newFs.close();

    conf.set("juicefs.umask", "000");
    newFs = createNewFs(conf, currentUser.getShortUserName(), currentUser.getGroupNames());
    newFs.delete(new Path("/test_umask"), true);
    newFs.mkdirs(new Path("/test_umask/dir"));
    newFs.create(new Path("/test_umask/dir/f")).close();
    assertEquals(FsPermission.createImmutable((short) 0777), newFs.getFileStatus(new Path("/test_umask")).getPermission());
    assertEquals(FsPermission.createImmutable((short) 0777), newFs.getFileStatus(new Path("/test_umask/dir")).getPermission());
    assertEquals(FsPermission.createImmutable((short) 0666), newFs.getFileStatus(new Path("/test_umask/dir/f")).getPermission());

    conf.set("juicefs.umask", "022");
    conf.set("fs.permissions.umask-mode", "077");
    Path p = new Path("/test_umask/u_parent/f");
    newFs = createNewFs(conf, currentUser.getShortUserName(), currentUser.getGroupNames());
    newFs.delete(p.getParent());
    FSDataOutputStream out = newFs.create(p, true);
    out.close();
    assertEquals(FsPermission.createImmutable((short) 0755), fs.getFileStatus(p.getParent()).getPermission());
    assertEquals(FsPermission.createImmutable((short) 0644), fs.getFileStatus(p).getPermission());

    newFs.close();
  }

  public void testGuidMapping() throws Exception {
    Configuration newConf = new Configuration(cfg);

    FSDataOutputStream ou = fs.create(new Path("/etc/users"));
    ou.write("foo:10000\n".getBytes());
    ou.close();
    newConf.set("juicefs.users", "/etc/users");

    FileSystem fooFs = createNewFs(newConf, "foo", new String[]{"nogrp"});
    Path f = new Path("/test_foo");
    fooFs.create(f).close();
    assertEquals("foo", fooFs.getFileStatus(f).getOwner());
    fooFs.close();

    ou = fs.create(new Path("/etc/users"));
    ou.write("foo:10001\n".getBytes());
    ou.close();
    fs.close();

    FileSystem newFS = FileSystem.newInstance(newConf);
    assertEquals("10000", newFS.getFileStatus(f).getOwner());
    newFS.delete(f, false);
    newFS.close();
  }

  public void testGuidMappingFromString() throws Exception {
    fs.close();
    Configuration newConf = new Configuration(cfg);

    newConf.set("juicefs.users", "bar:10000;foo:20000;baz:30000");
    newConf.set("juicefs.groups", "user:1000:foo,bar;admin:2000:baz");
    newConf.set("juicefs.superuser", UserGroupInformation.getCurrentUser().getShortUserName());

    FileSystem fooFs = createNewFs(newConf, "foo", new String[]{"nogrp"});
    Path f = new Path("/test_foo");
    fooFs.create(f).close();
    fooFs.setOwner(f, "foo", "user");
    assertEquals("foo", fooFs.getFileStatus(f).getOwner());
    assertEquals("user", fooFs.getFileStatus(f).getGroup());
    fooFs.close();

    newConf.set("juicefs.users", "foo:20001");
    newConf.set("juicefs.groups", "user:1001:foo,bar;admin:2001:baz");
    FileSystem newFS = FileSystem.newInstance(newConf);
    assertEquals("20000", newFS.getFileStatus(f).getOwner());
    assertEquals("1000", newFS.getFileStatus(f).getGroup());

    newFS.delete(f, false);
    newFS.close();
  }

  public void testTrash() throws Exception {
    Trash trash = new Trash(fs, cfg);
    Path trashFile = new Path("/tmp/trashfile");
    trash.expungeImmediately();
    fs.create(trashFile).close();
    Trash.moveToAppropriateTrash(fs, trashFile, cfg);
    trash.checkpoint();
    fs.create(trashFile).close();
    Trash.moveToAppropriateTrash(fs, trashFile, cfg);
    assertEquals(2, fs.listStatus(fs.getTrashRoot(trashFile)).length);
    trash.expungeImmediately();
    assertEquals(0, fs.listStatus(fs.getTrashRoot(trashFile)).length);
  }

  public void testBlockSize() throws Exception {
    Configuration newConf = new Configuration(cfg);
    newConf.set("dfs.blocksize", "256m");
    FileSystem newFs = FileSystem.newInstance(newConf);
    assertEquals(256 << 20, newFs.getDefaultBlockSize(new Path("/")));
  }

  public void testReadSpeed() throws Exception {
    int read = (128 << 10) ;
    Path speedFile = new Path("/tmp/speedFile");
    fs.delete(speedFile, false);
    FSDataOutputStream ou = fs.create(speedFile);
    int fileSize = 128 << 20;
    ou.write(new byte[fileSize]);
    ou.close();
    FSDataInputStream open = fs.open(speedFile);
    AtomicLong counter = new AtomicLong(0L);
    AtomicBoolean finished = new AtomicBoolean(false);
    TimerTask timerTask = new TimerTask() {
      @Override
      public void run() {
        System.out.printf("read method calls: %d\n", counter.get());
        finished.set(true);
      }
    };
    Timer timer = new Timer();
    timer.schedule(timerTask, 1000);

    ByteBuffer readArray = ByteBuffer.allocateDirect(read);
    while (!finished.get()) {
      open.seek(0);
      readArray.position(0);
      readArray.limit(read);
      open.read(readArray);
      counter.getAndIncrement();
    }
  }

  private void createFileWithContents(FileSystem fs, Path f, byte[] contents) throws IOException {
    try (FSDataOutputStream out = fs.create(f)) {
      if (contents != null) {
        out.write(contents);
      }
    }
  }

  public void testIOClosed() throws Exception {
    Path f = new Path("/tmp/closedFile");
    FSDataOutputStream ou = fs.create(f);
    ou.close();
    try {
      ou.write(new byte[1]);
      fail("should not work when write to a closed stream");
    } catch (IOException ignored) {
    }
    FSDataInputStream in = fs.open(f);
    in.close();
    try {
      in.read(new byte[1]);
      fail("should not work when read a closed stream");
    } catch (IOException ignored) {
    }

    ou = fs.create(f);
    ou.close();
    ou.close();
  }

  public void testRead() throws Exception {
    Path f = new Path("/tmp/posFile");
    int fileLen = 1 << 20;
    byte[] contents = new byte[fileLen];
    Random random = new Random();
    random.nextBytes(contents);
    createFileWithContents(fs, f, contents);
    FSDataInputStream in = fs.open(f);

    byte[] readBytes = new byte[fileLen];
    int got = in.read(readBytes);
    assertFalse(in.markSupported());
    assertEquals(fileLen, got);
    assertEquals(fileLen, in.getPos());
    assertArrayEquals(Arrays.copyOfRange(contents, 0, fileLen), readBytes);
    in.close();

    in = fs.open(f);
    int b = 0;
    int count = 0;
    while ((b = in.read()) != -1) {
      assertEquals(contents[count]&0xFF, b);
      count++;
    }
    assertEquals(fileLen, count);
    in.close();

    int readSize = 100;
    in = fs.open(f);
    got = in.read(new byte[readSize]);
    assertEquals(readSize, got);
    assertEquals(readSize, in.getPos());
    assertEquals(fileLen - readSize, in.available());
    in.close();

    in = fs.open(f);
    readBytes = new byte[128<<10];
    int off = 100;
    int len = 100;
    int read = in.read(readBytes, off, len);
    assertEquals(len, read);
    assertArrayEquals(Arrays.copyOfRange(contents, 0, len), Arrays.copyOfRange(readBytes, off, off + len));
    in.close();

    try {
      in = fs.open(f);
      in.read(readBytes, off, readBytes.length - off + 1);
      fail("IndexOutOfBoundsException");
    } catch (IndexOutOfBoundsException ignored) {
    } finally {
      in.close();
    }

    in = fs.open(f);
    in.seek(fileLen - 100);
    long skip = in.skip(100);
    assertEquals(100, skip);

    in.seek(fileLen - 100);
    skip = in.skip(fileLen - 100 + 1);
    assertEquals(100, skip);
    in.close();
  }

  public void testInnerSymlink() throws Exception {
    //echo "hello juicefs" > inner_sym_link
    FileStatus status = fs.getFileStatus(new Path("/inner_sym_link"));
    assertEquals("inner_sym_link", status.getPath().getName());
    assertEquals(14, status.getLen());
  }

  public void testUserWithMultiGroups() throws Exception {
    Path users = new Path("/etc/users");
    Path groups = new Path("/etc/groups_multi");

    writeFile(fs, users, "tom:2001\n");
    writeFile(fs, groups, "groupa:3001:tom\ngroupb:3002:tom");
    fs.close();

    Configuration conf = new Configuration(cfg);
    conf.set("juicefs.users", users.toUri().getPath());
    conf.set("juicefs.groups", groups.toUri().getPath());
    conf.set("juicefs.debug", "true");

    FileSystem superFs = createNewFs(conf, "hdfs", new String[]{"hadoop"});
    Path testDir = new Path("/test_multi_group/d1");
    superFs.mkdirs(testDir);
    superFs.setOwner(testDir.getParent(), "hdfs", "groupb");
    superFs.setOwner(testDir, "hdfs", "groupb");
    superFs.setPermission(testDir.getParent(), FsPermission.createImmutable((short) 0770));
    superFs.setPermission(testDir, FsPermission.createImmutable((short) 0770));

    FileSystem tomFs = createNewFs(conf, "tom", new String[]{"randgroup"});
    tomFs.listStatus(testDir);

    superFs.delete(testDir.getParent(), true);
    tomFs.close();
    superFs.close();
  }

  public void testConcurrentCreate() throws Exception {
    int threads = 100;
    ExecutorService pool = Executors.newFixedThreadPool(threads);
    for (int i = 0; i < threads; i++) {
      pool.submit(() -> {
        JuiceFileSystem jfs = new JuiceFileSystem();
        try {
          jfs.initialize(URI.create("jfs://dev/"), cfg);
          jfs.listStatus(new Path("/"));
          jfs.close();
        } catch (IOException e) {
          fail("concurrent create failed");
          System.exit(1);
        }
      });
    }
    pool.shutdown();
    pool.awaitTermination(1, TimeUnit.MINUTES);
  }

  private boolean tryAccess(Path path, String user, String[] group, FsAction action) throws Exception {
    UserGroupInformation testUser = UserGroupInformation.createUserForTesting(user, group);
    FileSystem fs = testUser.doAs((PrivilegedExceptionAction<FileSystem>) () -> {
      Configuration conf = new Configuration();
      conf.set("juicefs.grouping", "");
      return FileSystem.get(conf);
    });

    boolean canAccess;
    try {
      fs.access(path, action);
      canAccess = true;
    } catch (AccessControlException e) {
      canAccess = false;
    }
    return canAccess;
  }
  static AclEntry aclEntry(AclEntryScope scope, AclEntryType type, FsAction permission) {
    return new AclEntry.Builder().setScope(scope).setType(type).setPermission(permission).build();
  }

  static AclEntry aclEntry(AclEntryScope scope, AclEntryType type, String name, FsAction permission) {
    return new AclEntry.Builder().setScope(scope).setType(type).setName(name).setPermission(permission).build();
  }

  public void testAcl() throws Exception {
    List<AclEntry> acls = Lists.newArrayList(
        aclEntry(DEFAULT, USER, "foo", ALL)
    );
    Path p = new Path("/testacldir");
    fs.delete(p, true);
    fs.mkdirs(p);
    fs.setAcl(p, acls);
    Path childFile = new Path(p, "file");
    fs.create(childFile).close();
    assertTrue(tryAccess(childFile, "foo", new String[]{"nogrp"}, WRITE));
    assertFalse(tryAccess(childFile, "wrong", new String[]{"nogrp"}, WRITE));
    assertEquals(fs.getFileStatus(childFile).getPermission().getGroupAction(), READ_WRITE);

    Path childDir = new Path(p, "dir");
    fs.mkdirs(childDir);
    assertEquals(fs.getFileStatus(childDir).getPermission().getGroupAction(), ALL);
  }

  public void testAclException() throws Exception {
    List<AclEntry> acls = Lists.newArrayList(
        aclEntry(ACCESS, USER, "foo", ALL)
    );
    Path p = new Path("/test_acl_exception");
    fs.delete(p, true);
    fs.mkdirs(p);
    try {
      fs.setAcl(p, acls);
      fail("Invalid ACL: the user, group and other entries are required.");
    } catch (AclTransformation.AclException ignored) {
    }
  }

  public void testDefaultAclExistingDirFile() throws Exception {
    Path parent = new Path("/testDefaultAclExistingDirFile");
    fs.delete(parent, true);
    fs.mkdirs(parent);
    // the old acls
    List<AclEntry> acls1 = Lists.newArrayList(aclEntry(DEFAULT, USER, "foo", ALL));
    // the new acls
    List<AclEntry> acls2 = Lists.newArrayList(aclEntry(DEFAULT, USER, "foo", READ_EXECUTE));
    // set parent to old acl
    fs.setAcl(parent, acls1);

    Path childDir = new Path(parent, "childDir");
    fs.mkdirs(childDir);
    // the sub directory should also have the old acl
    AclEntry[] childDirExpectedAcl = new AclEntry[] { aclEntry(ACCESS, USER, "foo", ALL),
        aclEntry(ACCESS, GROUP, READ_EXECUTE), aclEntry(DEFAULT, USER, ALL),
        aclEntry(DEFAULT, USER, "foo", ALL), aclEntry(DEFAULT, GROUP, READ_EXECUTE),
        aclEntry(DEFAULT, MASK, ALL), aclEntry(DEFAULT, OTHER, READ_EXECUTE) };
    AclStatus childDirAcl = fs.getAclStatus(childDir);
    assertArrayEquals(childDirExpectedAcl, childDirAcl.getEntries().toArray());

    Path childFile = new Path(childDir, "childFile");
    // the sub file should also have the old acl
    fs.create(childFile).close();
    AclEntry[] childFileExpectedAcl = new AclEntry[] { aclEntry(ACCESS, USER, "foo", ALL),
        aclEntry(ACCESS, GROUP, READ_EXECUTE) };
    AclStatus childFileAcl = fs.getAclStatus(childFile);
    assertArrayEquals(childFileExpectedAcl, childFileAcl.getEntries().toArray());

    // now change parent to new acls
    fs.setAcl(parent, acls2);

    // sub directory and sub file should still have the old acls
    childDirAcl = fs.getAclStatus(childDir);
    assertArrayEquals(childDirExpectedAcl, childDirAcl.getEntries().toArray());
    childFileAcl = fs.getAclStatus(childFile);
    assertArrayEquals(childFileExpectedAcl, childFileAcl.getEntries().toArray());

    // now remove the parent acls
    fs.removeAcl(parent);

    // sub directory and sub file should still have the old acls
    childDirAcl = fs.getAclStatus(childDir);
    assertArrayEquals(childDirExpectedAcl, childDirAcl.getEntries().toArray());
    childFileAcl = fs.getAclStatus(childFile);
    assertArrayEquals(childFileExpectedAcl, childFileAcl.getEntries().toArray());

    // check changing the access mode of the file
    // mask out the access of group other for testing
    fs.setPermission(childFile, new FsPermission((short) 0640));
    boolean canAccess = tryAccess(childFile, "other", new String[] { "other" }, READ);
    assertFalse(canAccess);
    fs.delete(parent, true);
  }

  public void testAccessAclNotInherited() throws IOException {
    Path parent = new Path("/testAccessAclNotInherited");
    fs.delete(parent, true);
    fs.mkdirs(parent);
    // parent have both access acl and default acl
    List<AclEntry> acls = Lists.newArrayList(aclEntry(DEFAULT, USER, "foo", READ_EXECUTE),
        aclEntry(ACCESS, USER, ALL), aclEntry(ACCESS, GROUP, READ), aclEntry(ACCESS, OTHER, READ),
        aclEntry(ACCESS, USER, "bar", ALL));
    fs.setAcl(parent, acls);
    AclEntry[] expectedAcl = new AclEntry[] { aclEntry(ACCESS, USER, "bar", ALL), aclEntry(ACCESS, GROUP, READ),
        aclEntry(DEFAULT, USER, ALL), aclEntry(DEFAULT, USER, "foo", READ_EXECUTE),
        aclEntry(DEFAULT, GROUP, READ), aclEntry(DEFAULT, MASK, READ_EXECUTE), aclEntry(DEFAULT, OTHER, READ) };
    AclStatus dirAcl = fs.getAclStatus(parent);
    assertArrayEquals(expectedAcl, dirAcl.getEntries().toArray());

    Path childDir = new Path(parent, "childDir");
    fs.mkdirs(childDir);
    // subdirectory should only have the default acl inherited
    AclEntry[] childDirExpectedAcl = new AclEntry[] { aclEntry(ACCESS, USER, "foo", READ_EXECUTE),
        aclEntry(ACCESS, GROUP, READ), aclEntry(DEFAULT, USER, ALL),
        aclEntry(DEFAULT, USER, "foo", READ_EXECUTE), aclEntry(DEFAULT, GROUP, READ),
        aclEntry(DEFAULT, MASK, READ_EXECUTE), aclEntry(DEFAULT, OTHER, READ) };
    AclStatus childDirAcl = fs.getAclStatus(childDir);
    assertArrayEquals(childDirExpectedAcl, childDirAcl.getEntries().toArray());

    Path childFile = new Path(parent, "childFile");
    fs.create(childFile).close();
    // sub file should only have the default acl inherited
    AclEntry[] childFileExpectedAcl = new AclEntry[] { aclEntry(ACCESS, USER, "foo", READ_EXECUTE),
        aclEntry(ACCESS, GROUP, READ) };
    AclStatus childFileAcl = fs.getAclStatus(childFile);
    assertArrayEquals(childFileExpectedAcl, childFileAcl.getEntries().toArray());

    fs.delete(parent, true);
  }

  public void testFileStatusWithAcl() throws Exception {
    List<AclEntry> acls = Lists.newArrayList(
        aclEntry(ACCESS, USER, ALL),
        aclEntry(ACCESS, USER, "foo", ALL),
        aclEntry(ACCESS, OTHER, ALL),
        aclEntry(ACCESS, GROUP, ALL)
    );
    Path p = new Path("/test_acl_status");
    fs.delete(p, true);
    fs.mkdirs(p);
    FileStatus pStatus = fs.getFileStatus(p);
    assertFalse(pStatus.hasAcl());

    Path f = new Path(p, "f");
    fs.create(f).close();
    fs.setAcl(f, acls);
    FileStatus[] fileStatuses = fs.listStatus(p);
    assertTrue(fileStatuses[0].getPermission().getAclBit());
    assertTrue(fileStatuses[0].hasAcl());
  }

  public void testRenameAccessControlException() throws Exception {
    Path d1 = new Path("/renameAccessControlExceptionDir1");
    Path d2 = new Path("/renameAccessControlExceptionDir2");
    Path p = new Path(d1, "file");
    FileSystem user1Fs = createNewFs(cfg, "user1", new String[]{"group1"});

    user1Fs.mkdirs(d1);
    user1Fs.mkdirs(d2);
    user1Fs.create(p).close();
    user1Fs.setPermission(d1, new FsPermission((short) 0000));
    user1Fs.setPermission(d2, new FsPermission((short) 0777));
    try {
      user1Fs.rename(p, d2);
    } catch (AccessControlException e) {
      assertTrue(e.getMessage().contains("renameAccessControlExceptionDir1"));
    }

    user1Fs.setPermission(d1, new FsPermission((short) 0777));
    user1Fs.setPermission(d2, new FsPermission((short) 000));
    try {
      user1Fs.rename(p, d2);
    } catch (AccessControlException e) {
      assertTrue(e.getMessage().contains("renameAccessControlExceptionDir2"));
    }

    // clean
    user1Fs.setPermission(d1, new FsPermission((short) 0777));
    user1Fs.setPermission(d2, new FsPermission((short) 0777));
    user1Fs.delete(d1, true);
    user1Fs.delete(d2, true);
  }
}
