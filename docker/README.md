# Docker Setup for GeeseFS

This folder contains Docker configurations for testing GeeseFS with MinIO.

## Prerequisites

- Docker and Docker Compose
- [just](https://github.com/casey/just) command runner (recommended)

## Services

- **minio**: MinIO S3-compatible storage server
- **minio-init**: Initializes the test bucket with proper permissions
- **geesefs-1**: First GeeseFS container mounting the S3 bucket (with `--enable-symlinks-file`)
- **geesefs-2**: Second GeeseFS container mounting the same bucket (with `--enable-symlinks-file`)
- **conditional-write-test**: Python test container for S3 conditional writes
- **symlinks-test**: Shell script test container for symlinks file feature
- **test-client**: MinIO client for verifying bucket contents

## Quick Start with Just (Recommended)

The project includes a `justfile` with convenient commands. Run `just` to see all available recipes.

```bash
# From the docker/ directory
cd docker

# Show all available commands
just

# Start all services
just up

# Run all tests
just test

# Run specific tests
just test-conditional          # S3 conditional write tests
just test-symlinks             # Symlinks file tests
just test-geesefs-conditional  # GeeseFS conditional tests via FUSE

# View GeeseFS logs
just logs

# Open MinIO Console in browser
just console

# Test writing/reading across containers
just write-test
just read-test

# Test symlinks
just create-symlink
just read-symlink

# List bucket contents
just ls
just ls-recursive
just ls-symlinks

# Shell into containers
just shell-1   # GeeseFS container 1
just shell-2   # GeeseFS container 2
just shell-minio

# Check service health
just health

# Stop all services
just down

# Stop and remove volumes
just clean

# Rebuild containers
just rebuild        # Rebuild and restart GeeseFS containers
just rebuild-clean  # Full rebuild with clean volumes
just full-rebuild   # Clean, build, and start everything
```

## Quick Start with Docker Compose

If you prefer using Docker Compose directly:

```bash
# From the docker/ directory
cd docker

# Start all services
docker compose up -d

# Run conditional write tests
docker compose run --rm conditional-write-test

# Run symlinks file tests
docker compose run --rm symlinks-test

# View GeeseFS logs
docker compose logs geesefs-1 geesefs-2

# Access MinIO Console
open http://localhost:9001  # user: minioadmin, password: minioadmin

# Test writing from one container and reading from another
docker exec geesefs-mount-1 sh -c 'echo "Hello" > /mnt/s3/test.txt'
docker exec geesefs-mount-2 cat /mnt/s3/test.txt

# Test symlinks
docker exec geesefs-mount-1 sh -c 'echo "target content" > /mnt/s3/target.txt'
docker exec geesefs-mount-1 ln -s target.txt /mnt/s3/link.txt
docker exec geesefs-mount-2 cat /mnt/s3/link.txt

# Stop all services
docker compose down

# Stop and remove volumes
docker compose down -v
```

## Configuration

Environment variables for GeeseFS containers:

| Variable | Default | Description |
|----------|---------|-------------|
| `AWS_ACCESS_KEY_ID` | minioadmin | S3 access key |
| `AWS_SECRET_ACCESS_KEY` | minioadmin | S3 secret key |
| `S3_ENDPOINT` | <http://minio:9000> | S3 endpoint URL |
| `S3_BUCKET` | testbucket | Bucket to mount |
| `MOUNT_POINT` | /mnt/s3 | Mount location |
| `GEESEFS_OPTS` | --debug_s3 --enable-symlinks-file | Additional GeeseFS options |

## Symlinks File Tests

The `symlinks-test` container tests the `--enable-symlinks-file` feature which stores symlink metadata in a hidden `.symlinks` JSON file per directory instead of object metadata. This is useful for S3 backends that don't return UserMetadata in listings.

Tests include:

1. **Create symlink** - Verify `.symlinks` file is created with correct JSON structure
2. **Cross-container visibility** - Symlinks created in container 1 are visible in container 2
3. **Subdirectory symlinks** - Each directory has its own `.symlinks` file
4. **Delete symlink** - Verify `.symlinks` file is updated when symlink is removed
5. **Bidirectional sync** - Symlinks created in container 2 are visible in container 1
6. **Hidden file** - `.symlinks` file is not visible in directory listings

## Conditional Write Tests

The `conditional-write-test` container tests S3 conditional write features:

1. **If-None-Match: "*"** - Create only if object doesn't exist
2. **If-Match: \<etag\>** - Update only if ETag matches (optimistic locking)
3. **Concurrent write race** - 5 writers, only 1 succeeds
4. **Read-modify-write pattern** - Counter increments with retry on conflict

These features (added to S3 in August 2024) prevent race conditions when multiple writers access the same object.
