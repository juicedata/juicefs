package io.juicefs.utils;

import junit.framework.TestCase;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.net.URI;
import java.util.concurrent.*;

public class BgTaskUtilTest extends TestCase {
  private static final Logger LOG = LoggerFactory.getLogger(BgTaskUtilTest.class);

  public void testBgTask() throws Exception {
    String[] volNames = new String[]{"fs1", "fs2", "fs3"};
    String[] taskNames = new String[]{"task1", "task2", "task3"};
    int threads = 20;
    ExecutorService pool = Executors.newFixedThreadPool(threads);

    int instances = 100;
    CountDownLatch latch = new CountDownLatch(instances);

    for (int i = 0; i < instances; i++) {
      int handle = i + 1;
      pool.submit(() -> {
        String volName = volNames[ThreadLocalRandom.current().nextInt(100) % volNames.length];
        try {
          BgTaskUtil.register(volName, handle);
          BgTaskUtil.startTrashEmptier(volName, () -> {
            LOG.info("tid {} running trash empiter for {}", Thread.currentThread().getId(), volName);
            while (true) {
              try {
                Thread.sleep(100);
              } catch (InterruptedException e) {
                break;
              }
            }
          }, 0, TimeUnit.MINUTES);
          // put many tasks
          for (int j = 0; j < 10; j++) {
            String taskName = taskNames[ThreadLocalRandom.current().nextInt(100) % taskNames.length];
            BgTaskUtil.putTask(volName,
                taskName,
                () -> {
                  LOG.info("running {}|{}", volName, taskName);
                  try {
                    Thread.sleep(ThreadLocalRandom.current().nextInt(2000));
                  } catch (InterruptedException e) {
                    throw new RuntimeException(e);
                  }
                },
                0, 1, TimeUnit.MINUTES
            );
          }
        } catch (Exception e) {
          LOG.error("unexpected", e);
        } finally {
          BgTaskUtil.unregister(volName, handle, () -> {
            LOG.info("clean {}", volName);
          });
          latch.countDown();
        }
      });
    }
    latch.await();
    assertEquals(0, BgTaskUtil.getBgThreadForName().size());
    assertEquals(0, BgTaskUtil.getRunningInstance().size());
  }
}
