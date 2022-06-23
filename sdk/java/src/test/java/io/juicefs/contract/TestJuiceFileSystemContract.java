/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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