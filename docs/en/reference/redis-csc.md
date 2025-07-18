# Redis Client-Side Caching Support in JuiceFS

Starting with version 7.4, Redis provides [Client-Side Caching](https://redis.io/docs/latest/develop/reference/client-side-caching/) which allows clients to maintain local caches of data in a faster and more efficient way.

## How it works

Redis Client-Side Caching (CSC) works by:
1. The client enables tracking mode with `CLIENT TRACKING ON`
2. The client caches data locally after reading it from Redis
3. Redis notifies the client when cached keys are modified by any client
4. The client invalidates those keys in its local cache

This results in reduced network traffic, lower latency, and higher throughput.

## Configuration

JuiceFS supports Redis CSC through the following options in the metadata URL:

```
--meta-url="redis://localhost/1?client-cache=true" # Enable client-side caching (always BCAST mode) 
--meta-url="redis://localhost/1?client-cache=true&client-cache-size=50000" # Set cache size (default 100000) 
--meta-url="redis://localhost/1?client-cache-expire=30s" # Set cache expiration (default 30s)
```

### Options

- `client-cache`: Enables client-side caching in BCAST mode (set to any value except "false")
- `client-cache-size`: Maximum number of cached entries (default: 100000)
- `client-cache-expire`: Cache expiration time (default: 30 seconds)

## Modes

JuiceFS uses BCAST mode for simplicity and reliability:

- **BCAST mode**: All keys accessed by the client are tracked and notifications are sent for any changes.

BCAST mode provides the simplest implementation while ensuring cache coherence across all clients.

## Requirements

- Redis server version 7.4 or higher
- JuiceFS with CSC support enabled

## Performance Considerations

1. Avoid setting cache too large to prevent excessive memory usage
2. The cache is most effective for metadata-heavy workloads with many repeated operations
3. Write operations automatically invalidate related cache entries to maintain consistency
4. For very write-heavy workloads, consider disabling CSC as invalidation traffic may offset benefits

## Troubleshooting

If you experience crashes or instability with CSC enabled:

1. Update to the latest JuiceFS version which contains important fixes for CSC
2. Try reducing the cache size with `client-cache-size`
3. If problems persist, disable CSC by removing the `client-cache` parameter

## References

- [Redis Client-Side Caching Documentation](https://redis.io/docs/latest/develop/reference/client-side-caching/)
