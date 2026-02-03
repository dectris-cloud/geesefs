# GeeseFS Conditional Writes Test

This test validates that GeeseFS properly uses S3 conditional writes (If-Match/If-None-Match) when multiple mounts access the same files through the FUSE interface.

## What it tests

1. **If-None-Match behavior** - Create only if file doesn't exist
2. **If-Match behavior** - Update only if ETag matches
3. **Concurrent write conflict detection** between multiple mounts
4. **Cache invalidation** when files are modified by other mounts

## Running the test

```bash
# Run the test
docker compose up --build --abort-on-container-exit

# Or use just
just test

# Clean up
docker compose down -v
# Or
just clean
```

## Files

- `docker-compose.yml` - Docker Compose configuration with MinIO + 2 GeeseFS mounts
- `Dockerfile.geesefs` - Builds GeeseFS binary with FUSE support
- `docker-entrypoint.sh` - Container entrypoint script
- `test_geesefs_conditional.sh` - Shell test script
