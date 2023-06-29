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

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

public class BgTaskUtil {
  private static final Logger LOG = LoggerFactory.getLogger(BgTaskUtil.class);
  private static final ScheduledExecutorService threadPool = Executors.newScheduledThreadPool(2, r -> {
    Thread thread = new Thread(r, "Background Task");
    thread.setDaemon(true);
    return thread;
  });
  // use timer to run trash emptier because it will occupy a thread
  private static final List<Timer> timers = new ArrayList<>();
  private static Set<BgTaskKey> runningBgTask = new HashSet<>();

  static class BgTaskKey {
    String scheme;
    String authority;
    String type;

    public BgTaskKey(String scheme, String authority, String type) {
      this.scheme = scheme;
      this.authority = authority;
      this.type = type;
    }

    @Override
    public int hashCode() {
      return (scheme + authority + type).hashCode();
    }

    @Override
    public boolean equals(Object obj) {
      if (obj == this) {
        return true;
      }
      if (obj instanceof BgTaskKey) {
        BgTaskKey that = (BgTaskKey) obj;
        return Objects.equals(this.scheme, that.scheme)
            && Objects.equals(this.authority, that.authority)
            && Objects.equals(this.type, that.type);
      }
      return false;
    }
  }

  public static void startScheduleTask(String scheme, String authority, String type, Runnable task, long initialDelay, long period, TimeUnit unit) {
    synchronized (runningBgTask) {
      if (isRunning(scheme, authority, type)) {
        return;
      }
      threadPool.scheduleAtFixedRate(() -> {
        try {
          task.run();
        } catch (Exception e) {
          LOG.error("Background task failed", e);
        }
      }, initialDelay, period, unit);
      runningBgTask.add(new BgTaskKey(scheme, authority, type));
    }
  }

  public static void startTrashEmptier(String scheme, String authority, String type, Runnable emptierTask, long delay) {
    synchronized (runningBgTask) {
      if (isRunning(scheme, authority, type)) {
        return;
      }
      Timer timer = new Timer();
      timer.schedule(new TimerTask() {
        @Override
        public void run() {
          emptierTask.run();
        }
      }, delay);
      runningBgTask.add(new BgTaskKey(scheme, authority, type));
      timers.add(timer);
    }
  }

  public static boolean isRunning(String scheme, String authority, String type) {
    synchronized (runningBgTask) {
      return runningBgTask.contains(new BgTaskKey(scheme, authority, type));
    }
  }

  public static void close() {
    threadPool.shutdownNow();
    for (Timer timer : timers) {
      timer.cancel();
      timer.purge();
    }
  }
}
