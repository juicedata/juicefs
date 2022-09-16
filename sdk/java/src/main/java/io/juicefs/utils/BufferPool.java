package io.juicefs.utils;

import java.lang.ref.WeakReference;
import java.nio.ByteBuffer;
import java.util.Queue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.ConcurrentMap;

/**
 * thread safe
 */
public class BufferPool {

  private static final ConcurrentMap<Integer, Queue<WeakReference<ByteBuffer>>> buffersBySize = new ConcurrentHashMap<>();

  public static ByteBuffer getBuffer(int size) {
    Queue<WeakReference<ByteBuffer>> list = buffersBySize.get(size);
    if (list == null) {
      return ByteBuffer.allocate(size);
    }

    WeakReference<ByteBuffer> ref;
    while ((ref = list.poll()) != null) {
      ByteBuffer b = ref.get();
      if (b != null) {
        return b;
      }
    }

    return ByteBuffer.allocate(size);
  }

  public static void returnBuffer(ByteBuffer buf) {
    buf.clear();
    int size = buf.capacity();
    Queue<WeakReference<ByteBuffer>> list = buffersBySize.get(size);
    if (list == null) {
      list = new ConcurrentLinkedQueue<>();
      Queue<WeakReference<ByteBuffer>> prev = buffersBySize.putIfAbsent(size, list);
      // someone else put a queue in the map before we did
      if (prev != null) {
        list = prev;
      }
    }
    list.add(new WeakReference<>(buf));
  }
}