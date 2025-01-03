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

  private static final Map<String, ScheduledExecutorService> bgThreadForName = new HashMap<>(); // volName -> threadpool
  private static final Map<String, Object> tasks = new HashMap<>(); // volName|taskName -> running
  private static final Map<String, Set<Long>> runningInstance = new HashMap<>();

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
    synchronized (runningInstance) {
      LOG.debug("register instance for {}({})", volName, handle);
      if (!runningInstance.containsKey(volName)) {
        Set<Long> handles = new HashSet<>();
        handles.add(handle);
        runningInstance.put(volName, handles);
      } else {
        runningInstance.get(volName).add(handle);
      }
    }
  }

  public static void unregister(String volName, long handle, Runnable cleanupTask) {
    if (handle <= 0) {
      return;
    }
    synchronized (runningInstance) {
      if (!runningInstance.containsKey(volName)) {
        return;
      }
      Set<Long> handles = runningInstance.get(volName);
      boolean removed = handles.remove(handle);
      if (!removed) {
        return;
      }
      LOG.debug("unregister instance for {}({})", volName, handle);
      if (handles.size() == 0) {
        LOG.debug("clean resources for {}", volName);
        ScheduledExecutorService pool = bgThreadForName.remove(volName);
        if (pool != null) {
          pool.shutdownNow();
        }
        stopTrashEmptier(volName);
        tasks.entrySet().removeIf(e -> e.getKey().startsWith(volName + "|"));
        cleanupTask.run();
        runningInstance.remove(volName);
      }
    }
  }

  public  interface Task {
    void run() throws IOException;
  }


  public static void putTask(String volName, String taskName, Task task, long delay, long period, TimeUnit unit) throws IOException {
    synchronized (tasks) {
      String key = volName + "|" + taskName;
      if (!tasks.containsKey(key)) {
        LOG.debug("start task {}", key);
        task.run();
        // build background task thread for volume name
        ScheduledExecutorService pool = bgThreadForName.computeIfAbsent(volName,
            n -> Executors.newScheduledThreadPool(1, r -> {
              Thread thread = new Thread(r, "JuiceFS Background Task");
              thread.setDaemon(true);
              return thread;
            })
        );
        pool.scheduleAtFixedRate(()->{
          try {
            task.run();
          } catch (IOException e) {
            LOG.warn("run {} failed", key, e);
          }
        }, delay, period, unit);
        tasks.put(key, new Object());
      }
    }
  }

  public static void startTrashEmptier(String name, Runnable emptierTask, long delay, TimeUnit unit) {
    synchronized (tasks) {
      String key = name + "|" + "Trash emptier";
      if (!tasks.containsKey(key)) {
        LOG.debug("run trash emptier for {}", name);
        ScheduledExecutorService thread = Executors.newScheduledThreadPool(1);
        thread.schedule(emptierTask, delay, unit);
        tasks.put(key, thread);
      }
    }
  }

  private static void stopTrashEmptier(String name) {
    synchronized (tasks) {
      String key = name + "|" + "Trash emptier";
      Object v = tasks.remove(key);
      if (v instanceof ScheduledExecutorService) {
        LOG.debug("close trash emptier for {}", name);
        ((ScheduledExecutorService) v).shutdownNow();
      }
    }
  }
}
