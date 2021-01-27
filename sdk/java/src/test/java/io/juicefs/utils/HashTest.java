/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

package io.juicefs.utils;

import com.google.common.collect.Lists;
import junit.framework.TestCase;
import org.apache.commons.math3.stat.descriptive.SummaryStatistics;

import java.util.*;
import java.util.function.Function;
import java.util.stream.Collectors;

public class HashTest extends TestCase {
  private static List<String> PATHS = new ArrayList<String>() {
    {
      String prefix = "jfs:///tmp/file";
      for (int i = 0; i < 1_000; i++) {
        add(prefix + i);
      }
    }
  };

  public void testConsitentHashCompat() {
    ConsistentHash<String> hash = new ConsistentHash<>(100, Lists.newArrayList());
    hash.addNode("192.168.1.1");
    hash.addNode("192.168.2.2");
    hash.addNode("192.168.3.3");
    hash.addNode("192.168.4.4");
    assertEquals("192.168.3.3", hash.get("123-0"));
    assertEquals("192.168.4.4", hash.get("456-2"));
    assertEquals("192.168.2.2", hash.get("789-3"));
  }

  public void testConsitentHash() {
    ConsistentHash<String> hash = new ConsistentHash<>(100, getNodes());
    Map<String, String> before = new HashMap<>();
    Map<String, String> after = new HashMap<>();

    for (String path : PATHS) {
      before.put(path, hash.get(path));
    }

    hash.remove("Node4");
    for (String path : PATHS) {
      after.put(path, hash.get(path));
    }
    System.out.println("====== stdev");
    System.out.println("before:\t" + stdev(before));
    System.out.println("after:\t" + stdev(after));

    System.out.println("====== (max - min)/avg");
    Map<String, Long> collect = after.values().stream()
            .collect(Collectors.groupingBy(Function.identity(), Collectors.counting()));
    Long max = Collections.max(collect.values());
    Long min = Collections.min(collect.values());
    long sum = collect.values().stream().mapToLong(i -> i).sum();
    System.out.println((double) (max - min) / ((double) sum / getNodes().size()));

    int count = 0; // total count of path that was moved
    for (Map.Entry<String, String> entry : before.entrySet()) {
      String path = entry.getKey();
      String host = entry.getValue();
      if (!host.equals(after.get(path)))
        count++;
    }
    double moveRatio = (double) count / before.size();
    System.out.println("move ratio:\t" + moveRatio);

    assertTrue(moveRatio < (double) 2 / getNodes().size());
  }

  private static double stdev(Map<String, String> after) {
    Map<String, Long> collect = after.values().stream()
            .collect(Collectors.groupingBy(Function.identity(), Collectors.counting()));
    SummaryStatistics statistics = new SummaryStatistics();
    for (Long value : collect.values()) {
      statistics.addValue(value);
    }
    double sum = statistics.getSum();
    statistics.clear();
    for (Long value : collect.values()) {
      statistics.addValue((double) value / sum);
    }

    return statistics.getStandardDeviation();
  }

  private List<String> getNodes() {
    List<String> nodes = Lists.newArrayList();
    for (int i = 0; i < 100; i++) {
      nodes.add("Node" + i);
    }
    return nodes;
  }
}
