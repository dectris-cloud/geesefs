#!/bin/bash
set -e

S3_ENDPOINT="${S3_ENDPOINT:-http://minio:9000}"
S3_BUCKET="${S3_BUCKET:-testbucket}"
MOUNT_POINT="${MOUNT_POINT:-/mnt/s3}"
GEESEFS_OPTS="${GEESEFS_OPTS:-}"

mkdir -p "$MOUNT_POINT"

echo "GeeseFS multipart-copy boundary reproducer"
echo "=========================================="
echo "S3 Endpoint: $S3_ENDPOINT"
echo "Bucket:      $S3_BUCKET"
echo "Mount Point: $MOUNT_POINT"

MINIO_HOST=$(echo "$S3_ENDPOINT" | sed -E 's|https?://([^:/]+).*|\1|')
MINIO_PORT=$(echo "$S3_ENDPOINT" | sed -E 's|.*:([0-9]+).*|\1|')
if [ "$MINIO_PORT" = "$S3_ENDPOINT" ]; then
    MINIO_PORT=9000
fi

echo "Waiting for MinIO at $MINIO_HOST:$MINIO_PORT..."
for i in $(seq 1 30); do
    if nc -z "$MINIO_HOST" "$MINIO_PORT" 2>/dev/null; then
        echo "MinIO reachable."
        break
    fi
    sleep 1
done

if [ $# -gt 0 ]; then
    exec /geesefs "$@"
fi

echo "Mounting bucket '$S3_BUCKET' at '$MOUNT_POINT'..."
exec /geesefs \
    --endpoint "$S3_ENDPOINT" \
    -o allow_other \
    -f \
    $GEESEFS_OPTS \
    "$S3_BUCKET" "$MOUNT_POINT"
