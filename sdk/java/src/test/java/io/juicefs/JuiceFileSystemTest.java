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

import junit.framework.TestCase;
import org.apache.commons.io.IOUtils;
import org.apache.flink.runtime.fs.hdfs.HadoopRecoverableWriter;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsPermission;
import org.apache.hadoop.io.MD5Hash;
import org.apache.hadoop.security.UserGroupInformation;

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
    Thread.currentThread().interrupt();
    fs = FileSystem.get(cfg);
    Thread.interrupted();
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
    Path trg = new Path("/concat");
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
    fs.concat(trg, new Path[]{src1, src2} );
    FSDataInputStream in = fs.open(trg);
    assertEquals("hellohellohello", IOUtils.toString(in));
    in.close();
    // src should be deleted after concat
    assertFalse(fs.exists(src1));
    assertFalse(fs.exists(src2));
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
}
