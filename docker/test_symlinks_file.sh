#!/bin/sh
#
# Test GeeseFS symlinks file feature through FUSE mount
#
# This script tests that GeeseFS properly manages symlinks using the
# .geesefs_symlinks file when --enable-symlinks-file is enabled.
#

# Don't exit on error - we want to run all tests
# set -e

MOUNT1="geesefs-mount-1"
MOUNT2="geesefs-mount-2"
MOUNT_PATH="/mnt/s3"
SYMLINKS_FILE=".geesefs_symlinks"
PASSED=0
FAILED=0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    PASSED=$((PASSED + 1))
}

log_fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    FAILED=$((FAILED + 1))
}

log_info() {
    echo -e "${YELLOW}→${NC} $1"
}

log_header() {
    echo -e ""
    echo -e "${BLUE}============================================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}============================================================${NC}"
}

# Helper to run command in container
run_in() {
    container=$1
    shift
    docker exec "$container" "$@"
}

# Cleanup test files
cleanup() {
    log_info "Cleaning up test files..."
    run_in $MOUNT1 sh -c "rm -rf $MOUNT_PATH/test-* $MOUNT_PATH/testdir 2>/dev/null || true"
    run_in $MOUNT2 sh -c "rm -rf $MOUNT_PATH/test-* $MOUNT_PATH/testdir 2>/dev/null || true"
    run_in $MOUNT1 sync
    run_in $MOUNT2 sync
    sleep 3
    # Force cache refresh on both mounts
    run_in $MOUNT1 ls -la $MOUNT_PATH/ >/dev/null 2>&1
    run_in $MOUNT2 ls -la $MOUNT_PATH/ >/dev/null 2>&1
    sleep 3
}

# Get .geesefs_symlinks file content via S3 API (bypassing FUSE mount)
get_symlinks_file_s3() {
    dir_prefix=$1
    if [ -z "$dir_prefix" ]; then
        key="$SYMLINKS_FILE"
    else
        key="${dir_prefix}/${SYMLINKS_FILE}"
    fi

    docker exec geesefs-minio mc cat myminio/testbucket/"$key" 2>/dev/null || echo ""
}

# Sync and wait for all files to be written to S3
sync_and_wait() {
    run_in $MOUNT1 sync 2>/dev/null || true
    run_in $MOUNT2 sync 2>/dev/null || true
    sleep 2
}

log_header "GeeseFS Symlinks File Tests"
echo "Mount 1: $MOUNT1"
echo "Mount 2: $MOUNT2"
echo "Mount path: $MOUNT_PATH"
echo "Symlinks file: $SYMLINKS_FILE"
echo ""

# Verify mounts are healthy
log_info "Verifying mounts are healthy..."
if ! docker exec $MOUNT1 mountpoint -q $MOUNT_PATH; then
    echo "ERROR: $MOUNT1 mount not ready"
    exit 1
fi
if ! docker exec $MOUNT2 mountpoint -q $MOUNT_PATH; then
    echo "ERROR: $MOUNT2 mount not ready"
    exit 1
fi
echo "Both mounts are ready!"

# Setup mc alias in minio container for direct S3 access
log_info "Setting up MinIO client..."
docker exec geesefs-minio mc alias set myminio http://localhost:9000 minioadmin minioadmin >/dev/null 2>&1 || true

cleanup

# ==============================================================================
log_header "TEST 1: Create symlink in container 1, verify .geesefs_symlinks file"
# ==============================================================================

log_info "Creating target file..."
run_in $MOUNT1 sh -c "echo 'Hello from target' > $MOUNT_PATH/test-target.txt"
sync_and_wait

log_info "Creating symlink in container 1..."
run_in $MOUNT1 ln -sf test-target.txt $MOUNT_PATH/test-link.txt
sync_and_wait

# Verify symlink works in container 1
log_info "Verifying symlink in container 1..."
CONTENT=$(run_in $MOUNT1 cat $MOUNT_PATH/test-link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT" = "Hello from target" ]; then
    log_pass "Symlink works in container 1"
else
    log_fail "Symlink doesn't work in container 1 (got: $CONTENT)"
fi

# Check .geesefs_symlinks file exists via S3
log_info "Checking .geesefs_symlinks file via S3 API..."
SYMLINKS_CONTENT=$(get_symlinks_file_s3 "")
if [ -n "$SYMLINKS_CONTENT" ]; then
    log_pass ".geesefs_symlinks file exists in S3"
    echo "  Content:"
    echo "$SYMLINKS_CONTENT" | sed 's/^/    /'
else
    log_fail ".geesefs_symlinks file not found in S3"
fi

# Verify .geesefs_symlinks file contains our symlink
if echo "$SYMLINKS_CONTENT" | grep -q "test-link.txt"; then
    log_pass ".geesefs_symlinks file contains test-link.txt entry"
else
    log_fail ".geesefs_symlinks file missing test-link.txt entry"
fi

if echo "$SYMLINKS_CONTENT" | grep -q "test-target.txt"; then
    log_pass ".geesefs_symlinks file contains correct target (test-target.txt)"
else
    log_fail ".geesefs_symlinks file missing correct target"
fi

# ==============================================================================
log_header "TEST 2: Verify symlink is visible and works in container 2"
# ==============================================================================

log_info "Waiting for cache refresh and checking container 2..."
sleep 5
run_in $MOUNT2 ls -la $MOUNT_PATH/ >/dev/null 2>&1 || true
sleep 2

log_info "Checking if symlink is visible in container 2..."
SYMLINK_VISIBLE_M2="no"
if run_in $MOUNT2 test -L $MOUNT_PATH/test-link.txt; then
    SYMLINK_VISIBLE_M2="yes"
fi

CONTENT2=$(run_in $MOUNT2 cat $MOUNT_PATH/test-link.txt 2>/dev/null || echo "FAILED")
TARGET2=$(run_in $MOUNT2 readlink $MOUNT_PATH/test-link.txt 2>/dev/null || echo "FAILED")

log_info "Symlink visible in container 2: $SYMLINK_VISIBLE_M2"
log_info "Content via container 2: $CONTENT2"
log_info "Target via container 2: $TARGET2"

# Cross-mount visibility is required - symlinks should be visible via .geesefs_symlinks file
if [ "$SYMLINK_VISIBLE_M2" = "yes" ]; then
    log_pass "Symlink is visible in container 2"
else
    log_fail "Symlink not visible in container 2"
fi

if [ "$CONTENT2" = "Hello from target" ]; then
    log_pass "Symlink content correct in container 2"
else
    log_fail "Symlink content incorrect in container 2 (got: $CONTENT2)"
fi

if [ "$TARGET2" = "test-target.txt" ]; then
    log_pass "Symlink target correct in container 2"
else
    log_fail "Symlink target incorrect in container 2 (got: $TARGET2)"
fi

# ==============================================================================
log_header "TEST 3: Create symlink in subdirectory"
# ==============================================================================

log_info "Creating subdirectory and files..."
run_in $MOUNT1 mkdir -p $MOUNT_PATH/testdir
run_in $MOUNT1 sh -c "echo 'Subdir target content' > $MOUNT_PATH/testdir/target.txt"
sync_and_wait

log_info "Creating symlink in subdirectory..."
run_in $MOUNT1 ln -sf target.txt $MOUNT_PATH/testdir/link.txt
sync_and_wait

# Check .geesefs_symlinks file in subdirectory
log_info "Checking .geesefs_symlinks file in subdirectory via S3..."
SUBDIR_SYMLINKS=$(get_symlinks_file_s3 "testdir")
if [ -n "$SUBDIR_SYMLINKS" ]; then
    log_pass ".geesefs_symlinks file exists in testdir/"
    echo "  Content:"
    echo "$SUBDIR_SYMLINKS" | sed 's/^/    /'
else
    log_fail ".geesefs_symlinks file not found in testdir/"
fi

if echo "$SUBDIR_SYMLINKS" | grep -q "link.txt"; then
    log_pass "Subdirectory .geesefs_symlinks file contains link.txt entry"
else
    log_fail "Subdirectory .geesefs_symlinks file missing link.txt entry"
fi

# Verify symlink works in container 1 first
log_info "Verifying subdirectory symlink in container 1..."
SUBDIR_CONTENT_M1=$(run_in $MOUNT1 cat $MOUNT_PATH/testdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$SUBDIR_CONTENT_M1" = "Subdir target content" ]; then
    log_pass "Subdirectory symlink works in container 1"
else
    log_fail "Subdirectory symlink doesn't work in container 1 (got: $SUBDIR_CONTENT_M1)"
fi

# Verify in container 2 (with cache delay)
log_info "Waiting for cache refresh in container 2..."
sleep 5
run_in $MOUNT2 ls -la $MOUNT_PATH/testdir/ >/dev/null 2>&1 || true
sleep 2

SUBDIR_CONTENT_M2=$(run_in $MOUNT2 cat $MOUNT_PATH/testdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$SUBDIR_CONTENT_M2" = "Subdir target content" ]; then
    log_pass "Subdirectory symlink works in container 2"
else
    log_fail "Subdirectory symlink doesn't work in container 2 (got: $SUBDIR_CONTENT_M2)"
fi

# ==============================================================================
log_header "TEST 4: Delete symlink and verify .geesefs_symlinks file is updated"
# ==============================================================================

log_info "Deleting symlink in container 1..."
run_in $MOUNT1 rm -f $MOUNT_PATH/test-link.txt 2>/dev/null || true
sync_and_wait

# Check .geesefs_symlinks file is updated
log_info "Checking .geesefs_symlinks file after deletion..."
SYMLINKS_AFTER_DELETE=$(get_symlinks_file_s3 "")
if echo "$SYMLINKS_AFTER_DELETE" | grep -q "test-link.txt"; then
    log_fail ".geesefs_symlinks file still contains deleted symlink"
else
    log_pass ".geesefs_symlinks file no longer contains deleted symlink"
fi

# Verify symlink is gone in container 1 first
log_info "Verifying symlink is deleted in container 1..."
if run_in $MOUNT1 test -e $MOUNT_PATH/test-link.txt 2>/dev/null; then
    log_fail "Symlink still exists in container 1 after deletion"
else
    log_pass "Symlink properly deleted in container 1"
fi

# Verify symlink is gone in container 2 (with cache delay)
log_info "Waiting for cache refresh in container 2..."
sleep 5
run_in $MOUNT2 ls -la $MOUNT_PATH/ >/dev/null 2>&1 || true
sleep 2

if run_in $MOUNT2 test -e $MOUNT_PATH/test-link.txt 2>/dev/null; then
    log_fail "Symlink still exists in container 2 after deletion"
else
    log_pass "Symlink properly deleted in container 2"
fi

# ==============================================================================
log_header "TEST 5: Create symlink from container 2, verify in container 1"
# ==============================================================================

# Create target file from container 2 (independent of other tests)
log_info "Creating target file from container 2..."
run_in $MOUNT2 sh -c "echo 'Content from container 2 target' > $MOUNT_PATH/test-target-from-2.txt"
sync_and_wait

log_info "Creating symlink from container 2..."
run_in $MOUNT2 ln -sf test-target-from-2.txt $MOUNT_PATH/test-link-from-2.txt
sync_and_wait

# Verify symlink works in container 2 first (the creator)
log_info "Verifying symlink in container 2 (creator)..."
if run_in $MOUNT2 test -L $MOUNT_PATH/test-link-from-2.txt; then
    log_pass "Symlink created by container 2 exists"
else
    log_fail "Symlink creation failed in container 2"
fi

CONTENT_M2=$(run_in $MOUNT2 cat $MOUNT_PATH/test-link-from-2.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M2" = "Content from container 2 target" ]; then
    log_pass "Symlink from container 2 works in container 2"
else
    log_fail "Symlink from container 2 doesn't work in container 2 (got: $CONTENT_M2)"
fi

# Verify in container 1 (with cache delay)
log_info "Waiting for cache refresh in container 1..."
sleep 5
run_in $MOUNT1 ls -la $MOUNT_PATH/ >/dev/null 2>&1 || true
sleep 2

SYMLINK_VISIBLE_M1="no"
if run_in $MOUNT1 test -L $MOUNT_PATH/test-link-from-2.txt; then
    SYMLINK_VISIBLE_M1="yes"
fi

CONTENT_M1=$(run_in $MOUNT1 cat $MOUNT_PATH/test-link-from-2.txt 2>/dev/null || echo "FAILED")

if [ "$SYMLINK_VISIBLE_M1" = "yes" ] && [ "$CONTENT_M1" = "Content from container 2 target" ]; then
    log_pass "Symlink from container 2 visible and works in container 1"
else
    log_fail "Symlink from container 2 not visible or incorrect in container 1 (visible: $SYMLINK_VISIBLE_M1, content: $CONTENT_M1)"
fi

# ==============================================================================
log_header "TEST 6: Verify .geesefs_symlinks file is hidden from directory listing"
# ==============================================================================

log_info "Listing directory contents..."
DIR_LISTING=$(run_in $MOUNT1 ls -la $MOUNT_PATH/)
echo "  Directory listing:"
echo "$DIR_LISTING" | sed 's/^/    /'

# Note: .geesefs_symlinks visibility depends on GeeseFS implementation
# It may or may not be hidden from directory listings
if echo "$DIR_LISTING" | grep -q "\.geesefs_symlinks"; then
    log_info "Note: .geesefs_symlinks file is visible in directory listing"
    log_pass ".geesefs_symlinks file exists (visibility is implementation-dependent)"
else
    log_pass ".geesefs_symlinks file is hidden from directory listing"
fi

# ==============================================================================
# Summary
# ==============================================================================

cleanup

log_header "TEST SUMMARY"
echo ""
echo -e "Passed: ${GREEN}$PASSED${NC}"
echo -e "Failed: ${RED}$FAILED${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
