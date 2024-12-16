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
package io.juicefs;

import io.juicefs.utils.BgTaskUtil;
import junit.framework.TestCase;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.net.URI;
import java.util.Map;
import java.util.concurrent.*;

import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_CHECKPOINT_INTERVAL_KEY;
import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_INTERVAL_KEY;

public class JuiceFileSystemBgTaskTest extends TestCase {
  private static final Logger LOG = LoggerFactory.getLogger(JuiceFileSystemBgTaskTest.class);

  public void testJuiceFileSystemBgTask() throws Exception {
    FileSystem.closeAll();
    Configuration conf = new Configuration();
    conf.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    conf.set(FS_TRASH_INTERVAL_KEY, "6");
    conf.set(FS_TRASH_CHECKPOINT_INTERVAL_KEY, "2");
    conf.set("juicefs.users", "jfs://dev/users");
    conf.set("juicefs.groups", "jfs://dev/groups");
    conf.set("juicefs.discover-nodes-url", "jfs://dev/etc/nodes");
    int threads = 100;
    int instances = 1000;
    CountDownLatch latch = new CountDownLatch(instances);
    ExecutorService pool = Executors.newFixedThreadPool(threads);
    for (int i = 0; i < instances; i++) {
      pool.submit(() -> {
        try (JuiceFileSystem jfs = new JuiceFileSystem()) {
          jfs.initialize(URI.create("jfs://dev/"), conf);
          if (ThreadLocalRandom.current().nextInt(10) % 2 == 0) {
            jfs.getFileBlockLocations(jfs.getFileStatus(new Path("jfs://dev/users")), 0, 1000);
          }
        } catch (Exception e) {
          LOG.error("unexpected exception", e);
        } finally {
          latch.countDown();
        }
      });
    }
    latch.await();
    Map<String, ScheduledExecutorService> bgThreadForName = BgTaskUtil.getBgThreadForName();
    for (String s : bgThreadForName.keySet()) {
      System.out.println(s);
    }
    assertEquals(0, bgThreadForName.size());
    assertEquals(0, BgTaskUtil.getRunningInstance().size());
    pool.shutdown();
  }
}
