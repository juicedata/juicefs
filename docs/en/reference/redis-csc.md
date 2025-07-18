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
--meta-url="redis://localhost/1?client-cache=true&client-cache-size=500" # Set cache size in MB (default 300MB) 
--meta-url="redis://localhost/1?client-cache-expire=30s" # Set cache expiration (default: infinite)
```

### Options

- `client-cache`: Enables client-side caching in BCAST mode (set to any value except "false")
- `client-cache-size`: Maximum cache size in megabytes (default: 300MB)
- `client-cache-expire`: Optional cache expiration time (default: infinite - entries stay in cache until invalidated by server or evicted due to size limits)

When client-side caching is enabled, JuiceFS caches:
1. **Inode attributes**: File/directory metadata like permissions, size, timestamps
2. **Directory entries**: Name to inode mappings for faster lookups
3. **File chunks**: Read operations benefit from chunk-level caching for faster file access

> **Note:** Redis Client Side Cache requires Redis server version 7.4 or higher. Using this feature with older Redis versions will result in errors.

### Preloading Cache

When client-side caching is enabled, JuiceFS automatically preloads up to 10,000 inodes into the cache after mounting. This lazy preloading happens in the background and helps to:

1. Warm up the cache for common operations
2. Reduce latency for initial filesystem operations
3. Provide better performance from the moment the filesystem is mounted

The preloading process intelligently prioritizes the most important inodes by:
1. Starting with the root directory
2. Loading the most frequently accessed top-level directories and files
3. Recursively exploring important subdirectories

If the filesystem contains fewer than 10,000 inodes, only the most important ones will be preloaded. The preloading process runs in a background goroutine with fail-safe mechanisms and won't block or affect normal filesystem operations.

## Modes

JuiceFS uses BCAST mode for simplicity and reliability:

- **BCAST mode**: All keys accessed by the client are tracked and notifications are sent for any changes.

BCAST mode provides the simplest implementation while ensuring cache coherence across all clients.

## Requirements

- Redis server version 7.4 or higher
- JuiceFS with CSC support enabled

## Performance Considerations

1. The default 300MB cache size should be sufficient for most workloads
2. For very large filesystems with millions of files, you may benefit from increasing the cache size
3. The cache is most effective for metadata-heavy workloads with many repeated operations
4. Write operations automatically invalidate related cache entries to maintain consistency
5. For very write-heavy workloads, consider disabling CSC as invalidation traffic may offset benefits
6. The automatic preloading of up to 10,000 inodes helps achieve optimal performance from the start
7. File read operations benefit from chunk-level caching to improve read performance for frequently accessed files

## Troubleshooting

If you experience crashes or instability with CSC enabled:

1. Update to the latest JuiceFS version which contains important fixes for CSC
2. Try reducing the cache size with `client-cache-size`
3. Check Redis server logs for any memory or client tracking issues
4. Make sure your Redis server version is 7.4 or higher
5. If problems persist, disable CSC by removing the `client-cache` parameter

JuiceFS includes robust error handling for various Redis CSC-specific responses to ensure stable operation even when Redis sends unexpected response formats due to client tracking.

## References

- [Redis Client-Side Caching Documentation](https://redis.io/docs/latest/develop/reference/client-side-caching/)
