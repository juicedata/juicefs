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

import com.google.common.hash.HashFunction;
import com.google.common.hash.Hashing;

import java.util.List;
import java.util.SortedMap;
import java.util.concurrent.ConcurrentSkipListMap;

public class ConsistentHash<T> {

  private final int numberOfVirtualNodeReplicas;
  private final SortedMap<Integer, T> circle = new ConcurrentSkipListMap<>();
  private final HashFunction nodeHash = Hashing.murmur3_32();
  private final HashFunction keyHash = Hashing.murmur3_32();

  public ConsistentHash(int numberOfVirtualNodeReplicas, List<T> nodes) {
    this.numberOfVirtualNodeReplicas = numberOfVirtualNodeReplicas;
    addNode(nodes);
  }

  public void addNode(List<T> nodes) {
    for (T node : nodes) {
      addNode(node);
    }
  }

  public void addNode(T node) {
    for (int i = 0; i < numberOfVirtualNodeReplicas; i++) {
      circle.put(getKetamaHash(i + "" + node), node);
    }
  }

  public void remove(List<T> nodes) {
    for (T node : nodes) {
      remove(node);
    }
  }

  public void remove(T node) {
    for (int i = 0; i < numberOfVirtualNodeReplicas; i++) {
      circle.remove(getKetamaHash(i + "" + node));
    }
  }

  public T get(Object key) {
    if (circle.isEmpty()) {
      return null;
    }
    int hash = getKeyHash(key.toString());
    if (!circle.containsKey(hash)) {
      SortedMap<Integer, T> tailMap = circle.tailMap(hash);
      hash = tailMap.isEmpty() ? circle.firstKey() : tailMap.firstKey();
    }
    return circle.get(hash);
  }

  private int getKeyHash(final String k) {
    return keyHash.hashBytes(k.getBytes()).asInt();
  }

  private int getKetamaHash(final String k) {
    return nodeHash.hashBytes(k.getBytes()).asInt();
  }
}
