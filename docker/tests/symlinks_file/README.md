# GeeseFS Symlinks File Test

This test validates the `.geesefs_symlinks` file feature for cross-mount symlink visibility.

## What it tests

1. **Symlink creation** - Creates symlinks and verifies `.geesefs_symlinks` file is written
2. **Cross-mount visibility** - Symlinks created on mount 1 should be visible on mount 2
3. **Nested directories** - Symlinks work in subdirectories
4. **Symlink deletion** - Deleting a symlink updates the `.geesefs_symlinks` file
5. **Bidirectional visibility** - Symlinks created on mount 2 should be visible on mount 1
6. **Hidden file** - `.geesefs_symlinks` file is hidden from directory listings

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
- `test_symlinks_file.sh` - Shell test script

## Debugging

```bash
# Start services without running test
just up

# Shell into mount 1
just shell-1

# Shell into mount 2
just shell-2

# View GeeseFS logs
just logs

# Check symlinks files in bucket
just ls-symlinks
```
