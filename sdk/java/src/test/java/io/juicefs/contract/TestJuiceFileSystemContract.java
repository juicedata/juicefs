/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
package io.juicefs.contract;

import io.juicefs.JuiceFileSystemTest;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.apache.hadoop.fs.permission.FsPermission;
import org.junit.Before;
import org.junit.Test;

import java.io.IOException;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;
import static org.junit.Assume.assumeNotNull;

public class TestJuiceFileSystemContract extends FileSystemContractBaseTest {
  @Before
  public void setUp() throws Exception {
    Configuration cfg = new Configuration();
    cfg.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    Thread.currentThread().interrupt();
    fs = FileSystem.get(cfg);
    assumeNotNull(fs);
  }

  public FileSystem createNewFs(Configuration conf) throws IOException {
    return FileSystem.newInstance(FileSystem.getDefaultUri(conf), conf);
  }

  @Test
  public void testMkdirsWithUmask() throws Exception {
    Configuration conf = new Configuration(fs.getConf());
    conf.set(CommonConfigurationKeys.FS_PERMISSIONS_UMASK_KEY, TEST_UMASK);
    FileSystem newFs = createNewFs(conf);
    try {
      final Path dir = path("newDir");
      assertTrue(newFs.mkdirs(dir, new FsPermission((short) 0777)));
      FileStatus status = newFs.getFileStatus(dir);
      assertTrue(status.isDirectory());
      assertEquals((short) 0715, status.getPermission().toShort());
    } finally {
      newFs.close();
    }
  }
}