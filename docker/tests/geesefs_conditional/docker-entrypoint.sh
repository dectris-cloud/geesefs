#!/bin/bash
set -e

# Default values
S3_ENDPOINT="${S3_ENDPOINT:-http://minio:9000}"
S3_BUCKET="${S3_BUCKET:-testbucket}"
MOUNT_POINT="${MOUNT_POINT:-/mnt/s3}"
GEESEFS_OPTS="${GEESEFS_OPTS:-}"

# Ensure mount point exists
mkdir -p "$MOUNT_POINT"

echo "GeeseFS Docker Container"
echo "========================"
echo "S3 Endpoint: $S3_ENDPOINT"
echo "Bucket: $S3_BUCKET"
echo "Mount Point: $MOUNT_POINT"
echo ""

# Wait for MinIO to be reachable
echo "Waiting for MinIO at $S3_ENDPOINT..."
MINIO_HOST=$(echo "$S3_ENDPOINT" | sed -E 's|https?://([^:/]+).*|\1|')
MINIO_PORT=$(echo "$S3_ENDPOINT" | sed -E 's|.*:([0-9]+).*|\1|')
if [ "$MINIO_PORT" = "$S3_ENDPOINT" ]; then
    MINIO_PORT=9000
fi

for i in $(seq 1 30); do
    if nc -z "$MINIO_HOST" "$MINIO_PORT" 2>/dev/null; then
        echo "MinIO is reachable!"
        break
    fi
    echo "  Attempt $i/30: waiting for $MINIO_HOST:$MINIO_PORT..."
    sleep 1
done

# If arguments are passed, run geesefs with those arguments
if [ $# -gt 0 ]; then
    echo "Running: /geesefs $@"
    exec /geesefs "$@"
fi

# Default: mount the S3 bucket
echo "Mounting S3 bucket '$S3_BUCKET' to '$MOUNT_POINT'..."
echo "Running: /geesefs --endpoint $S3_ENDPOINT -o allow_other -f $GEESEFS_OPTS $S3_BUCKET $MOUNT_POINT"

exec /geesefs \
    --endpoint "$S3_ENDPOINT" \
    -o allow_other \
    -f \
    $GEESEFS_OPTS \
    "$S3_BUCKET" "$MOUNT_POINT"
