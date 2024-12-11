package io.juicefs;

import io.juicefs.utils.BgTaskUtil;
import junit.framework.TestCase;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;

import java.net.URI;
import java.util.Map;
import java.util.concurrent.*;

import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_CHECKPOINT_INTERVAL_KEY;
import static org.apache.hadoop.fs.CommonConfigurationKeysPublic.FS_TRASH_INTERVAL_KEY;

public class JuiceFileSystemBgTaskTest extends TestCase {
  public void testJuiceFileSystemBgTask() throws Exception {
    FileSystem.closeAll();
    Configuration conf = new Configuration();
    conf.addResource(JuiceFileSystemTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    conf.set(FS_TRASH_INTERVAL_KEY, "6");
    conf.set(FS_TRASH_CHECKPOINT_INTERVAL_KEY, "2");
    conf.set("juicefs.users", "jfs://dev/etc/users");
    conf.set("juicefs.groups", "jfs://dev/etc/groups");
    conf.set("juicefs.discover-nodes-url", "jfs://dev/etc/nodes");
    int threads = 100;
    int instances = 1000;
    CountDownLatch latch = new CountDownLatch(instances);
    ExecutorService pool = Executors.newFixedThreadPool(threads);
    for (int i = 0; i < instances; i++) {
      pool.submit(() -> {
        try (JuiceFileSystem jfs = new JuiceFileSystem()) {
          jfs.initialize(URI.create("jfs://dev/"), conf);
          if (ThreadLocalRandom.current().nextInt(10)%2==0) {
            jfs.getFileBlockLocations(jfs.getFileStatus(new Path("jfs://dev/etc/users")), 0, 1000);
          }
        } catch (Exception e) {
          fail("unexpected exception");
        }
        latch.countDown();
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
