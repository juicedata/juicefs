/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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


package io.juicefs.permission;

import io.juicefs.JuiceFileSystemTest;
import junit.framework.TestCase;
import org.apache.commons.io.IOUtils;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsAction;
import org.apache.hadoop.fs.permission.FsPermission;
import org.apache.hadoop.security.AccessControlException;
import org.apache.hadoop.security.UserGroupInformation;
import org.junit.Assert;

import java.io.ByteArrayOutputStream;
import java.security.PrivilegedExceptionAction;

public class RangerPermissionCheckerTest extends TestCase {

  private FileSystem fs;
  private Configuration cfg;

  public void setUp() throws Exception {
    cfg = new Configuration();
    cfg.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    cfg.set("juicefs.ranger-rest-url", "http://localhost");
    cfg.set("juicefs.ranger-service-name", "cl1_hadoop");
    // set superuser
    cfg.set("juicefs.superuser", UserGroupInformation.getCurrentUser().getShortUserName());
    fs = FileSystem.newInstance(cfg);
    cfg.setQuietMode(false);
  }

  public void tearDown() throws Exception {
    fs.close();
  }

  public void testRangerCheckerInitFailed() throws Exception {
    Configuration cfg1 = new Configuration();
    cfg1.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    cfg1.set("juicefs.superuser", UserGroupInformation.getCurrentUser().getShortUserName());
    cfg1.setQuietMode(false);

    FileSystem fs1 = FileSystem.newInstance(cfg1);

    final Path file = new Path("/tmp/tmpdir/data-file2");
    FSDataOutputStream out = fs1.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    fs1.setPermission(file, new FsPermission(FsAction.READ_WRITE, FsAction.READ, FsAction.NONE));

    // Now try to read the file as unknown user "bob" - ranger should allow this user, but now should not be allowed
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg1);
        try {
          fs.open(file);
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }

        fs.close();
        return null;
      }
    });

    fs1.delete(file);
    fs1.close();
  }

  public void testRead() throws Exception {
    HDFSReadTest("/tmp/tmpdir/data-file2");
  }

  public void testWrite() throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path("/tmp/tmpdir2/data-file3");
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    fs.setPermission(file, new FsPermission(FsAction.READ_WRITE, FsAction.READ_WRITE, FsAction.NONE));

    // Now try to write to the file as "bob" - this should be allowed (by the policy - user)
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Write to the file
        fs.append(file);
        fs.close();
        return null;
      }
    });

    // Now try to write to the file as "alice" - this should be allowed (by the policy - group)
    ugi = UserGroupInformation.createUserForTesting("alice", new String[]{"IT"});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Write to the file
        fs.append(file);
        fs.close();
        return null;
      }
    });

    // Now try to read the file as unknown user "eve" - this should not be allowed
    ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Write to the file
        try {
          fs.append(file);
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          // expected
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }
        fs.close();
        return null;
      }
    });

    fs.delete(file);
  }

  public void testExecute() throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path("/tmp/tmpdir3/data-file2");
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    fs.setPermission(file, new FsPermission(FsAction.READ_WRITE, FsAction.READ, FsAction.NONE));

    Path parentDir = new Path("/tmp/tmpdir3");

    fs.setPermission(parentDir, new FsPermission(FsAction.ALL, FsAction.READ_EXECUTE, FsAction.NONE));


    // Try to read the directory as "bob" - this should be allowed (by the policy - user)
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        RemoteIterator<LocatedFileStatus> iter = fs.listFiles(file.getParent(), false);
        Assert.assertTrue(iter.hasNext());

        fs.close();
        return null;
      }
    });
    // Try to read the directory as "alice" - this should be allowed (by the policy - group)
    ugi = UserGroupInformation.createUserForTesting("alice", new String[]{"IT"});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        RemoteIterator<LocatedFileStatus> iter = fs.listFiles(file.getParent(), false);
        Assert.assertTrue(iter.hasNext());
        fs.close();
        return null;
      }
    });

    // Now try to read the directory as unknown user "eve" - this should not be allowed
    ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        try {
          RemoteIterator<LocatedFileStatus> iter = fs.listFiles(file.getParent(), false);
          Assert.assertTrue(iter.hasNext());
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }

        fs.close();
        return null;
      }
    });

    fs.delete(file);
    fs.delete(parentDir);
  }

  public void testSetPermission() throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path("/tmp/tmpdir123/data-file3");
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    // Now try to read the file as unknown user "eve" - this will not find in ranger, and fallback check by origin Mask which should fail
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Write to the file
        try {
          fs.setPermission(file, new FsPermission(FsAction.READ, FsAction.NONE, FsAction.NONE));
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          // expected
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }
        fs.close();
        return null;
      }
    });

    fs.delete(file);
  }

  public void testSetOwner() throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path("/tmp/tmpdir123/data-file3");
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    // Now try to read the file as unknown user "eve" - this will not find in ranger, and fallback check by origin Mask which should fail
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Write to the file
        try {
          fs.setOwner(file, "eve", "eve");
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          // expected
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }
        fs.close();
        return null;
      }
    });

    fs.delete(file);
  }

  public void testReadTestUsingTagPolicy() throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path("/tmp/tmpdir6/data-file2");
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    fs.setPermission(file, new FsPermission(FsAction.READ_WRITE, FsAction.READ, FsAction.NONE));

    // Now try to read the file as "bob" - this should be allowed (by the policy - user)
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Read the file
        FSDataInputStream in = fs.open(file);
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        IOUtils.copy(in, output);
        String content = new String(output.toByteArray());
        Assert.assertTrue(content.startsWith("data0"));
        fs.close();
        return null;
      }
    });

    // Now try to read the file as "alice" - this should be allowed (by the policy - group)
    ugi = UserGroupInformation.createUserForTesting("alice", new String[]{"IT"});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Read the file
        FSDataInputStream in = fs.open(file);
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        IOUtils.copy(in, output);
        String content = new String(output.toByteArray());
        Assert.assertTrue(content.startsWith("data0"));

        fs.close();
        return null;
      }
    });

    // Now try to read the file as unknown user "eve" - this should not be allowed
    ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Read the file
        try {
          fs.open(file);
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          // expected
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }
        fs.close();
        return null;
      }
    });

    // Now try to read the file as known user "dave" - this should not be allowed, as he doesn't have the correct permissions
    ugi = UserGroupInformation.createUserForTesting("dave", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);

        // Read the file
        try {
          fs.open(file);
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          // expected
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }

        fs.close();
        return null;
      }
    });

    fs.delete(file);
  }

  public void testHDFSContentSummary() throws Exception {
    HDFSGetContentSummary("/tmp/get-content-summary");
    fs.delete(new Path("/tmp/get-content-summary"), true);
  }

  void HDFSReadTest(String fileName) throws Exception {

    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    final Path file = new Path(fileName);
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();

    fs.setPermission(file, new FsPermission(FsAction.READ_WRITE, FsAction.READ, FsAction.NONE));

    // Now try to read the file as "bob" - this should be allowed (by the policy - user)
    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        // Read the file
        FSDataInputStream in = fs.open(file);
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        IOUtils.copy(in, output);
        String content = new String(output.toByteArray());
        Assert.assertTrue(content.startsWith("data0"));

        fs.close();
        return null;
      }
    });

    // Now try to read the file as "alice" - this should be allowed (by the policy - group)
    ugi = UserGroupInformation.createUserForTesting("alice", new String[]{"IT"});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        FSDataInputStream in = fs.open(file);
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        IOUtils.copy(in, output);
        String content = new String(output.toByteArray());
        Assert.assertTrue(content.startsWith("data0"));
        fs.close();
        return null;
      }
    });

    // Now try to read the file as unknown user "eve" - this should not be allowed
    ugi = UserGroupInformation.createUserForTesting("eve", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {
      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        try {
          fs.open(file);
          Assert.fail("Failure expected on an incorrect permission");
        } catch (AccessControlException ex) {
          Assert.assertTrue(AccessControlException.class.getName().equals(ex.getClass().getName()));
        }

        fs.close();
        return null;
      }
    });

    fs.delete(file);
  }

  void HDFSGetContentSummary(final String dirName) throws Exception {

    String subdirName = dirName + "/tmpdir";

    createFile(subdirName, 1);
    createFile(subdirName, 2);

    fs.setPermission(new Path(dirName), new FsPermission(FsAction.READ_WRITE, FsAction.READ, FsAction.NONE));

    UserGroupInformation ugi = UserGroupInformation.createUserForTesting("bob", new String[]{});
    ugi.doAs(new PrivilegedExceptionAction<Void>() {

      public Void run() throws Exception {
        FileSystem fs = FileSystem.get(cfg);
        try {
          // GetContentSummary on the directory dirName
          ContentSummary contentSummary = fs.getContentSummary(new Path(dirName));

          long directoryCount = contentSummary.getDirectoryCount();
          Assert.assertTrue("Found unexpected number of directories; expected-count=3, actual-count=" + directoryCount, directoryCount == 3);
        } catch (Exception e) {
          Assert.fail("Failed to getContentSummary, exception=" + e);
        }
        fs.close();
        return null;
      }
    });

    deleteFile(subdirName, 1);
    deleteFile(subdirName, 2);
  }

  void createFile(String baseDir, Integer index) throws Exception {
    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    String dirName = baseDir + (index != null ? String.valueOf(index) : "");
    String fileName = dirName + "/dummy-data";
    final Path file = new Path(fileName);
    FSDataOutputStream out = fs.create(file);
    for (int i = 0; i < 1024; ++i) {
      out.write(("data" + i + "\n").getBytes("UTF-8"));
      out.flush();
    }
    out.close();
  }

  void deleteFile(String baseDir, Integer index) throws Exception {
    // Write a file - the AccessControlEnforcer won't be invoked as we are the "superuser"
    String dirName = baseDir + (index != null ? String.valueOf(index) : "");
    String fileName = dirName + "/dummy-data";
    final Path file = new Path(fileName);
    fs.delete(file);
  }
}
