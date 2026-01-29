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
