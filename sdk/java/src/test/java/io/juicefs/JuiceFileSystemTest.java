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

import junit.framework.TestCase;
import org.apache.commons.io.IOUtils;
import org.apache.flink.runtime.fs.hdfs.HadoopRecoverableWriter;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsAction;
import org.apache.hadoop.fs.permission.FsPermission;
import org.apache.hadoop.io.MD5Hash;
import org.apache.hadoop.security.AccessControlException;
import org.apache.hadoop.security.UserGroupInformation;

import java.io.FileNotFoundException;
import java.io.IOException;
import java.net.URI;
import java.nio.ByteBuffer;
import java.security.PrivilegedExceptionAction;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

import static org.junit.Assert.assertArrayEquals;

public class JuiceFileSystemTest extends TestCase {
  FsShell shell;
  FileSystem fs;
  Configuration cfg;

  public void setUp() throws Exception {
    cfg = new Configuration();
    cfg.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    fs = FileSystem.get(cfg);
    fs.delete(new Path("/hello"));
    FSDataOutputStream out = fs.create(new Path("/hello"), true);
    out.writeBytes("hello\n");
    out.close();

    cfg.setQuietMode(false);
    shell = new FsShell(cfg);
  }

  public void tearDown() throws Exception {
    fs.close();
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
    for (int i = 0; i < 5000; i++) {
      fs.mkdirs(new Path("/mkdirs/d" + i));
    }
    assertEquals(5001, fs.listStatus(new Path("/mkdirs/")).length);
    assertTrue(fs.delete(new Path("/mkdirs"), true));
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

    ou = fs.create(new Path("/etc/users"));
    ou.write("foo:10001\n".getBytes());
    ou.close();
    FileSystem newFS = FileSystem.newInstance(newConf);
    assertEquals("10000", fooFs.getFileStatus(f).getOwner());

    fooFs.delete(f, false);
  }
}
