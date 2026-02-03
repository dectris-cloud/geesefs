# Conditional Writes Test

This test validates S3 conditional write operations (If-Match / If-None-Match) directly against MinIO.

## What it tests

1. **If-None-Match: "*"** - Create only if object doesn't exist
2. **If-Match: <etag>** - Update only if ETag matches (optimistic locking)
3. **Concurrent write conflict detection**

These features were added to S3 in August 2024 and are useful for:

- Preventing race conditions when multiple writers access the same object
- Implementing optimistic locking patterns
- Ensuring atomic create-if-not-exists operations

## Running the test

```bash
# Run the test
docker compose up --build

# Or run interactively
docker compose run --rm test

# Clean up
docker compose down -v
```

## Files

- `docker-compose.yml` - Docker Compose configuration
- `Dockerfile` - Test container with Python and boto3
- `test_conditional_writes.py` - Python test script
