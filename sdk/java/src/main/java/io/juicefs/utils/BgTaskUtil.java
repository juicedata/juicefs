/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
package io.juicefs.utils;

import org.apache.hadoop.fs.FileSystem;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.*;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

public class BgTaskUtil {
  private static final Logger LOG = LoggerFactory.getLogger(BgTaskUtil.class);

  private static BgTaskUtil staticFieldForGc = new BgTaskUtil();

  private BgTaskUtil() {
  }

  private static final ScheduledExecutorService threadPool = Executors.newScheduledThreadPool(2, r -> {
    Thread thread = new Thread(r, "Background Task");
    thread.setDaemon(true);
    return thread;
  });
  // use timer to run trash emptier because it will occupy a thread
  private static final List<Timer> timers = new ArrayList<>();
  private static final List<FileSystem> fileSystems = new ArrayList<>();
  private static final Set<String> runningBgTask = new HashSet<>();

  public interface Task {
    void run() throws Exception;
  }

  public static void startScheduleTask(String name, String type, Task task, long initialDelay, long period, TimeUnit unit) {
    synchronized (runningBgTask) {
      if (isRunning(name, type)) {
        return;
      }
      threadPool.scheduleAtFixedRate(() -> {
        try {
          LOG.debug("Background task started for {} {}", name, type);
          task.run();
        } catch (Exception e) {
          LOG.warn("Background task failed for {} {}", name, type, e);
          synchronized (runningBgTask) {
            runningBgTask.remove(genKey(name, type));
          }
          throw new RuntimeException(e);
        }
      }, initialDelay, period, unit);
      runningBgTask.add(genKey(name, type));
    }
  }


  public static void startTrashEmptier(String name, String type, FileSystem fs, Runnable emptierTask, long delay) {
    synchronized (runningBgTask) {
      if (isRunning(name, type)) {
        return;
      }
      Timer timer = new Timer("trash emptier", true);
      timer.schedule(new TimerTask() {
        @Override
        public void run() {
          emptierTask.run();
        }
      }, delay);
      runningBgTask.add(genKey(name, type));
      timers.add(timer);
      fileSystems.add(fs);
    }
  }

  public static boolean isRunning(String name, String type) {
    synchronized (runningBgTask) {
      return runningBgTask.contains(genKey(name, type));
    }
  }

  private static String genKey(String name, String type) {
    return name + "|" + type;
  }

  @Override
  protected void finalize() {
    threadPool.shutdownNow();
    for (Timer timer : timers) {
      timer.cancel();
      timer.purge();
    }
    for (FileSystem fs : fileSystems) {
      try {
        fs.close();
      } catch (IOException e) {
        LOG.warn("close trash emptier fs failed", e);
      }
    }
  }
}
