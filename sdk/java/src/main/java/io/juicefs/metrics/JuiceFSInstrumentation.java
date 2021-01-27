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

package io.juicefs.metrics;

import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.metrics2.MetricsSystem;
import org.apache.hadoop.metrics2.annotation.Metric;
import org.apache.hadoop.metrics2.annotation.Metrics;
import org.apache.hadoop.metrics2.lib.DefaultMetricsSystem;

import java.io.IOException;
import java.util.HashMap;
import java.util.Map;


@Metrics(context = "JuiceFileSystem", name = "client")
public final class JuiceFSInstrumentation {
  private static MetricsSystem system;
  private static final String METRIC_NAME = "JuiceFSMetrics";

  private static int numFileSystems;

  private final Map<String, Long> valueState = new HashMap<>();
  private final Map<String, Long> timeState = new HashMap<>();

  static {
    system = DefaultMetricsSystem.initialize("juicefs");
  }

  private final FileSystem fs;
  private final FileSystem.Statistics statistics;

  @Metric("number of bytes read from JuiceFS")
  public long getBytesRead() {
    return statistics.getBytesRead();
  }

  @Metric("number of bytes write to JuiceFS")
  public double getBytesWrite() {
    return statistics.getBytesWritten();
  }

  @Metric("write speed")
  public synchronized double getBytesWritePerSec() {
    return getSpeedPerSec("writeSpeed", statistics.getBytesWritten());
  }


  @Metric("read speed")
  public synchronized double getBytesReadPerSec() {
    return getSpeedPerSec("readSpeed", statistics.getBytesRead());
  }

  @Metric("JuiceFS client num")
  public synchronized int getNumFileSystems() {
    return 1;
  }

  @Metric("JuiceFS used size")
  public synchronized long getUsedSize() {
    try {
      return fs.getStatus(new Path("/")).getUsed();
    } catch (IOException e) {
      return 0;
    }
  }

  @Metric("JuiceFS files")
  public synchronized long getFiles() {
    try {
      return fs.getContentSummary(new Path("/")).getFileCount();
    } catch (IOException e) {
      return 0;
    }
  }

  @Metric("JuiceFS dirs")
  public synchronized long getDirs() {
    try {
      return fs.getContentSummary(new Path("/")).getDirectoryCount();
    } catch (IOException e) {
      return 0;
    }
  }

  public double getSpeedPerSec(String name, long currentValue) {
    double speed = 0;
    long current = System.currentTimeMillis();
    long delta = current - timeState.getOrDefault(name, current);
    if (delta > 0) {
      speed = (currentValue - valueState.getOrDefault(name, currentValue)) / (delta / 1000.0);
    }
    valueState.put(name, currentValue);
    timeState.put(name, current);
    return speed;
  }

  public static synchronized void init(FileSystem fs, FileSystem.Statistics statistics) {
    if (numFileSystems == 0) {
      DefaultMetricsSystem.instance().register(METRIC_NAME, "JuiceFS client metrics",
              new JuiceFSInstrumentation(fs, statistics));
    }
    numFileSystems++;
  }

  private JuiceFSInstrumentation(FileSystem fs, FileSystem.Statistics statistics) {
    this.fs = fs;
    this.statistics = statistics;
  }

  public static synchronized void close() throws IOException {
    if (numFileSystems == 1) {
      system.publishMetricsNow();
      system.unregisterSource(METRIC_NAME);
    }
    numFileSystems--;
  }
}
