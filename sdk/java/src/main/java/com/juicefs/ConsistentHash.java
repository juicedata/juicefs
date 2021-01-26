package com.juicefs;

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
