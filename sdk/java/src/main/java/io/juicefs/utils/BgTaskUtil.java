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
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

public class BgTaskUtil {
  private static final Logger LOG = LoggerFactory.getLogger(BgTaskUtil.class);

  private static final Map<String, ScheduledExecutorService> bgThreadForName = new ConcurrentHashMap<>(); // volName -> threadpool
  private static final Map<String, Object> tasks = new ConcurrentHashMap<>(); // volName|taskName -> running
  private static final Map<String, Set<Long>> runningInstance = new ConcurrentHashMap<>();

  public static Map<String, ScheduledExecutorService> getBgThreadForName() {
    return bgThreadForName;
  }

  public static Map<String, Set<Long>> getRunningInstance() {
    return runningInstance;
  }

  public static void register(String volName, long handle) {
    if (handle <= 0) {
      return;
    }
    LOG.debug("register instance for {}({})", volName, handle);
    runningInstance.compute(volName, (k, v) -> {
      if (v == null) {
        LOG.debug("init resources for {}", volName);
        Set<Long> handles = new HashSet<>();
        handles.add(handle);
        return handles;
      }
      v.add(handle);
      return v;
    });
  }

  public static void unregister(String volName, long handle, Runnable cleanupTask) {
    if (handle <= 0) {
      return;
    }
    LOG.debug("unregister instance for {}({})", volName, handle);
    runningInstance.computeIfPresent(volName, (k, handles) -> {
      boolean removed = handles.remove(handle);
      if (!removed) {
        return handles;
      }
      if (handles.size() == 0) {
        LOG.debug("clean resources for {}", volName);
        ScheduledExecutorService pool = bgThreadForName.remove(volName);
        if (pool != null) {
          pool.shutdownNow();
        }
        stopTrashEmptier(volName);
        tasks.entrySet().removeIf(e -> e.getKey().startsWith(volName + "|"));
        cleanupTask.run();
        return null;
      }
      return handles;
    });
  }


  public static void putTask(String volName, String taskName, Runnable task, long delay, long period, TimeUnit unit) {
    tasks.compute(volName + "|" + taskName, (k, v) -> {
      if (v == null) {
        LOG.debug("start task {}|{}", volName, taskName);
        task.run();
        // build background task thread for volume name
        ScheduledExecutorService pool = bgThreadForName.computeIfAbsent(volName,
            n -> Executors.newScheduledThreadPool(1, r -> {
              Thread thread = new Thread(r, "JuiceFS Background Task");
              thread.setDaemon(true);
              return thread;
            })
        );
        pool.scheduleAtFixedRate(task, delay, period, unit);
        return new Object();
      }
      return v;
    });

  }

  static class TrashEmptyTask {
    FileSystem fs;
    ScheduledExecutorService thread;

    public TrashEmptyTask(FileSystem fs, ScheduledExecutorService thread) {
      this.fs = fs;
      this.thread = thread;
    }
  }

  public static void startTrashEmptier(String name, FileSystem fs, Runnable emptierTask, long delay, TimeUnit unit) {
    tasks.computeIfAbsent(name + "|" + "Trash emptier", k -> {
      LOG.debug("start trash emptier for {}", name);
      ScheduledExecutorService thread = Executors.newScheduledThreadPool(1);
      thread.schedule(emptierTask, delay, unit);
      return new TrashEmptyTask(fs, thread);
    });
  }

  private static void stopTrashEmptier(String name) {
    tasks.computeIfPresent(name + "|" + "Trash emptier", (k, v) -> {
      if (v instanceof TrashEmptyTask) {
        LOG.debug("close trash emptier for {}", name);
        ((TrashEmptyTask) v).thread.shutdownNow();
        try {
          ((TrashEmptyTask) v).fs.close();
        } catch (IOException e) {
          LOG.warn("close failed", e);
        }
      }
      return null;
    });
  }
}
