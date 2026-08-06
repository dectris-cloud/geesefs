#!/bin/sh
#
# Test GeeseFS symlinks file feature through FUSE mount
#
# This script tests that GeeseFS properly manages symlinks using the
# .geesefs_symlinks file when --enable-symlinks-file is enabled.
#
# Each test runs in its own isolated subfolder to prevent interference.
#

MOUNT1="symlinks-test-mount-1"
MOUNT2="symlinks-test-mount-2"
MINIO="symlinks-test-minio"
MOUNT_PATH="/mnt/s3"
SYMLINKS_FILE=".geesefs_symlinks"
PASSED=0
FAILED=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_pass() {
    printf "${GREEN}✓ PASS${NC}: %s\n" "$1"
    PASSED=$((PASSED + 1))
}

log_fail() {
    printf "${RED}✗ FAIL${NC}: %s\n" "$1"
    FAILED=$((FAILED + 1))
}

log_info() {
    printf "${YELLOW}→${NC} %s\n" "$1"
}

log_header() {
    printf "\n"
    printf "${BLUE}============================================================${NC}\n"
    printf "${BLUE}%s${NC}\n" "$1"
    printf "${BLUE}============================================================${NC}\n"
}

run_in() {
    container=$1
    shift
    docker exec "$container" "$@"
}

setup_test_folder() {
    test_num=$1
    TEST_DIR="$MOUNT_PATH/test$test_num"
    log_info "Setting up isolated test folder: $TEST_DIR"

    # Clean up from MOUNT1 first and wait for sync
    run_in $MOUNT1 rm -rf "$TEST_DIR" 2>/dev/null || true
    run_in $MOUNT1 sync
    sleep 3

    # Then clean up from MOUNT2 to handle any stale cache
    # MOUNT2 needs time to see the change and avoid race conditions
    run_in $MOUNT2 rm -rf "$TEST_DIR" 2>/dev/null || true
    run_in $MOUNT2 sync
    sleep 3

    # Now create directory from MOUNT1
    run_in $MOUNT1 mkdir -p "$TEST_DIR"
    run_in $MOUNT1 sync
    sleep 3

    # Verify creation
    if ! run_in $MOUNT1 test -d "$TEST_DIR"; then
        log_fail "Failed to create test directory $TEST_DIR"
        log_info "Debug: listing $MOUNT_PATH..."
        run_in $MOUNT1 ls -la "$MOUNT_PATH" || true
        return 1
    fi
}

cleanup_test_folder() {
    test_num=$1
    test_dir="$MOUNT_PATH/test$test_num"
    log_info "Cleaning up test folder: $test_dir"
    run_in $MOUNT1 rm -rf "$test_dir" 2>/dev/null || true
    run_in $MOUNT2 rm -rf "$test_dir" 2>/dev/null || true
    run_in $MOUNT1 sync
    run_in $MOUNT2 sync
    sleep 1
}

get_symlinks_file_s3() {
    dir_prefix=$1
    if [ -z "$dir_prefix" ]; then
        key="$SYMLINKS_FILE"
    else
        key="${dir_prefix}/${SYMLINKS_FILE}"
    fi
    docker exec $MINIO mc cat myminio/testbucket/"$key" 2>/dev/null || echo ""
}

sync_and_wait() {
    run_in $MOUNT1 sync 2>/dev/null || true
    run_in $MOUNT2 sync 2>/dev/null || true
    sleep 2
}

log_header "GeeseFS Symlinks File Tests"
echo "Mount 1: $MOUNT1"
echo "Mount 2: $MOUNT2"
echo "Mount path: $MOUNT_PATH"
echo ""

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

log_info "Setting up MinIO client..."
docker exec $MINIO mc alias set myminio http://localhost:9000 minioadmin minioadmin >/dev/null 2>&1 || true

# ==============================================================================
log_header "TEST 1: Create symlink, verify .geesefs_symlinks file"
# ==============================================================================

setup_test_folder 1

log_info "Creating target file..."
run_in $MOUNT1 sh -c "echo 'Hello from target' > $TEST_DIR/target.txt"
sync_and_wait

# Debug: Verify target file was created
if ! run_in $MOUNT1 test -f $TEST_DIR/target.txt; then
    log_fail "Target file was not created"
fi

log_info "Creating symlink in container 1..."
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/link.txt
SYMLINK_RC=$?
log_info "ln -sf exit code: $SYMLINK_RC"
sync_and_wait

# Debug: Show directory contents after symlink creation
log_info "Debug: Directory contents after symlink creation:"
run_in $MOUNT1 ls -la $TEST_DIR/ 2>&1 | sed 's/^/    /'

log_info "Verifying symlink in container 1..."
CONTENT=$(run_in $MOUNT1 cat $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT" = "Hello from target" ]; then
    log_pass "Symlink works in container 1"
else
    log_fail "Symlink doesn't work in container 1 (got: $CONTENT)"
fi

log_info "Checking .geesefs_symlinks file via S3 API..."
SYMLINKS_CONTENT=$(get_symlinks_file_s3 "test1")
if [ -n "$SYMLINKS_CONTENT" ]; then
    log_pass ".geesefs_symlinks file exists in S3"
    echo "  Content:"
    echo "$SYMLINKS_CONTENT" | sed 's/^/    /'
else
    log_fail ".geesefs_symlinks file not found in S3"
fi

if echo "$SYMLINKS_CONTENT" | grep -q "link.txt"; then
    log_pass ".geesefs_symlinks file contains link.txt entry"
else
    log_fail ".geesefs_symlinks file missing link.txt entry"
fi

if echo "$SYMLINKS_CONTENT" | grep -q "target.txt"; then
    log_pass ".geesefs_symlinks file contains correct target"
else
    log_fail ".geesefs_symlinks file missing correct target"
fi

cleanup_test_folder 1

# ==============================================================================
log_header "TEST 2: Cross-mount visibility (container 1 -> container 2)"
# ==============================================================================

setup_test_folder 2

log_info "Creating target file in container 1..."
run_in $MOUNT1 sh -c "echo 'Cross-mount content' > $TEST_DIR/target.txt"
sync_and_wait

log_info "Creating symlink in container 1..."
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/link.txt
sync_and_wait

log_info "Verifying symlink in container 1..."
CONTENT1=$(run_in $MOUNT1 cat $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT1" = "Cross-mount content" ]; then
    log_pass "Symlink works in container 1"
else
    log_fail "Symlink doesn't work in container 1 (got: $CONTENT1)"
fi

log_info "Waiting for cache refresh..."
sleep 5

# Debug: Check .geesefs_symlinks file via S3 before checking container 2
log_info "Debug: Checking .geesefs_symlinks via S3 API..."
SYMLINKS_S3=$(get_symlinks_file_s3 "test2")
if [ -n "$SYMLINKS_S3" ]; then
    log_info "S3 symlinks file content:"
    echo "$SYMLINKS_S3" | sed 's/^/    /'
else
    log_info "Warning: .geesefs_symlinks not found in S3!"
fi

log_info "Debug: Container 2 directory listing before access:"
run_in $MOUNT2 ls -la $TEST_DIR/ 2>&1 | sed 's/^/    /'
sleep 2

log_info "Checking symlink visibility in container 2..."
SYMLINK_VISIBLE="no"
if run_in $MOUNT2 test -L $TEST_DIR/link.txt; then
    SYMLINK_VISIBLE="yes"
fi

CONTENT2=$(run_in $MOUNT2 cat $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")
TARGET2=$(run_in $MOUNT2 readlink $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")

log_info "Symlink visible: $SYMLINK_VISIBLE, content: $CONTENT2, target: $TARGET2"

if [ "$SYMLINK_VISIBLE" = "yes" ]; then
    log_pass "Symlink is visible in container 2"
else
    log_fail "Symlink not visible in container 2"
fi

if [ "$CONTENT2" = "Cross-mount content" ]; then
    log_pass "Symlink content correct in container 2"
else
    log_fail "Symlink content incorrect in container 2 (got: $CONTENT2)"
fi

if [ "$TARGET2" = "target.txt" ]; then
    log_pass "Symlink target correct in container 2"
else
    log_fail "Symlink target incorrect in container 2 (got: $TARGET2)"
fi

cleanup_test_folder 2

# ==============================================================================
log_header "TEST 3: Symlink in nested subdirectory"
# ==============================================================================

setup_test_folder 3

log_info "Creating nested subdirectory..."
run_in $MOUNT1 mkdir -p $TEST_DIR/subdir
run_in $MOUNT1 sh -c "echo 'Nested content' > $TEST_DIR/subdir/target.txt"
sync_and_wait

log_info "Creating symlink in subdirectory..."
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/subdir/link.txt
sync_and_wait

log_info "Checking .geesefs_symlinks in subdirectory..."
SUBDIR_SYMLINKS=$(get_symlinks_file_s3 "test3/subdir")
if [ -n "$SUBDIR_SYMLINKS" ]; then
    log_pass ".geesefs_symlinks file exists in subdir"
    echo "  Content:"
    echo "$SUBDIR_SYMLINKS" | sed 's/^/    /'
else
    log_fail ".geesefs_symlinks file not found in subdir"
fi

if echo "$SUBDIR_SYMLINKS" | grep -q "link.txt"; then
    log_pass "Subdirectory symlinks file contains link.txt"
else
    log_fail "Subdirectory symlinks file missing link.txt"
fi

log_info "Verifying subdirectory symlink in container 1..."
CONTENT_M1=$(run_in $MOUNT1 cat $TEST_DIR/subdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M1" = "Nested content" ]; then
    log_pass "Subdirectory symlink works in container 1"
else
    log_fail "Subdirectory symlink doesn't work in container 1 (got: $CONTENT_M1)"
fi

log_info "Waiting for cache refresh..."
sleep 5
run_in $MOUNT2 ls -la $TEST_DIR/subdir/ >/dev/null 2>&1 || true
sleep 2

CONTENT_M2=$(run_in $MOUNT2 cat $TEST_DIR/subdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M2" = "Nested content" ]; then
    log_pass "Subdirectory symlink works in container 2"
else
    log_fail "Subdirectory symlink doesn't work in container 2 (got: $CONTENT_M2)"
fi

cleanup_test_folder 3

# ==============================================================================
log_header "TEST 4: Delete symlink updates .geesefs_symlinks"
# ==============================================================================

setup_test_folder 4

log_info "Creating target and symlink..."
run_in $MOUNT1 sh -c "echo 'Delete test' > $TEST_DIR/target.txt"
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/link.txt
sync_and_wait

log_info "Verifying symlink exists..."
if run_in $MOUNT1 test -L $TEST_DIR/link.txt; then
    log_info "Symlink exists"
else
    log_fail "Symlink creation failed"
fi

log_info "Deleting symlink..."
run_in $MOUNT1 rm -f $TEST_DIR/link.txt 2>/dev/null || true
sync_and_wait

log_info "Checking .geesefs_symlinks after deletion..."
SYMLINKS_AFTER=$(get_symlinks_file_s3 "test4")
if echo "$SYMLINKS_AFTER" | grep -q "link.txt"; then
    log_fail ".geesefs_symlinks still contains deleted symlink"
else
    log_pass ".geesefs_symlinks no longer contains deleted symlink"
fi

log_info "Verifying symlink deleted in container 1..."
if run_in $MOUNT1 test -e $TEST_DIR/link.txt 2>/dev/null; then
    log_fail "Symlink still exists in container 1"
else
    log_pass "Symlink properly deleted in container 1"
fi

log_info "Waiting for cache refresh..."
sleep 5
run_in $MOUNT2 ls -la $TEST_DIR/ >/dev/null 2>&1 || true
sleep 2

if run_in $MOUNT2 test -e $TEST_DIR/link.txt 2>/dev/null; then
    log_fail "Symlink still exists in container 2"
else
    log_pass "Symlink properly deleted in container 2"
fi

cleanup_test_folder 4

# ==============================================================================
log_header "TEST 5: Cross-mount visibility (container 2 -> container 1)"
# ==============================================================================

setup_test_folder 5

log_info "Creating target file from container 2..."
run_in $MOUNT2 sh -c "echo 'From container 2' > $TEST_DIR/target.txt"
sync_and_wait

log_info "Creating symlink from container 2..."
run_in $MOUNT2 ln -sf target.txt $TEST_DIR/link.txt
sync_and_wait

log_info "Verifying symlink in container 2..."
if run_in $MOUNT2 test -L $TEST_DIR/link.txt; then
    log_pass "Symlink created by container 2 exists"
else
    log_fail "Symlink creation failed in container 2"
fi

CONTENT_M2=$(run_in $MOUNT2 cat $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M2" = "From container 2" ]; then
    log_pass "Symlink works in container 2"
else
    log_fail "Symlink doesn't work in container 2 (got: $CONTENT_M2)"
fi

log_info "Waiting for cache refresh in container 1..."
sleep 5
run_in $MOUNT1 ls -la $TEST_DIR/ >/dev/null 2>&1 || true
sleep 2

SYMLINK_VISIBLE="no"
if run_in $MOUNT1 test -L $TEST_DIR/link.txt; then
    SYMLINK_VISIBLE="yes"
fi

CONTENT_M1=$(run_in $MOUNT1 cat $TEST_DIR/link.txt 2>/dev/null || echo "FAILED")

if [ "$SYMLINK_VISIBLE" = "yes" ] && [ "$CONTENT_M1" = "From container 2" ]; then
    log_pass "Symlink from container 2 visible and works in container 1"
else
    log_fail "Symlink from container 2 not visible/incorrect in container 1 (visible: $SYMLINK_VISIBLE, content: $CONTENT_M1)"
fi

cleanup_test_folder 5

# ==============================================================================
log_header "TEST 6: .geesefs_symlinks file is hidden by default"
# ==============================================================================

setup_test_folder 6

log_info "Creating symlink..."
run_in $MOUNT1 sh -c "echo 'Visibility test' > $TEST_DIR/target.txt"
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/link.txt
sync_and_wait

log_info "Listing directory contents (expecting .geesefs_symlinks to be hidden)..."
DIR_LISTING=$(run_in $MOUNT1 ls -la $TEST_DIR/)
echo "  Directory listing:"
echo "$DIR_LISTING" | sed 's/^/    /'

if echo "$DIR_LISTING" | grep -q "\.geesefs_symlinks"; then
    log_fail ".geesefs_symlinks file is visible but should be hidden by default"
else
    log_pass ".geesefs_symlinks file is hidden from listing (--hide-symlinks-file=true by default)"
fi

log_info "Verifying .geesefs_symlinks exists via S3 API (bypassing FUSE)..."
SYMLINKS_CONTENT=$(get_symlinks_file_s3 "test6")
if [ -n "$SYMLINKS_CONTENT" ]; then
    log_pass ".geesefs_symlinks file exists in S3 but is hidden from FUSE listing"
else
    log_fail ".geesefs_symlinks file not found in S3"
fi

cleanup_test_folder 6

# ==============================================================================
log_header "TEST 7: Direct access to symlink in subfolder without listing"
# ==============================================================================

setup_test_folder 7

log_info "Creating nested subdirectory and target file from container 1..."
run_in $MOUNT1 mkdir -p $TEST_DIR/deep/nested
run_in $MOUNT1 sh -c "echo 'Direct access content' > $TEST_DIR/deep/nested/target.txt"
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/deep/nested/link.txt
sync_and_wait

log_info "Verifying symlink exists via S3 API..."
SYMLINKS_S3=$(get_symlinks_file_s3 "test7/deep/nested")
if [ -n "$SYMLINKS_S3" ]; then
    log_pass ".geesefs_symlinks file exists in S3 for nested dir"
    echo "  Content:"
    echo "$SYMLINKS_S3" | sed 's/^/    /'
else
    log_fail ".geesefs_symlinks file not found in S3 for nested dir"
fi

log_info "Waiting for cache refresh in container 2..."
sleep 5

log_info "Directly reading symlink content from container 2 (no ls or cd first)..."
CONTENT_DIRECT=$(run_in $MOUNT2 cat $TEST_DIR/deep/nested/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_DIRECT" = "Direct access content" ]; then
    log_pass "Symlink in subfolder accessible without listing directory first (container 2)"
else
    log_fail "Symlink in subfolder NOT accessible without listing directory (got: $CONTENT_DIRECT)"
fi

log_info "Directly checking symlink type from container 2 (no ls or cd first)..."
if run_in $MOUNT2 test -L $TEST_DIR/deep/nested/link.txt; then
    log_pass "Symlink is recognized as symlink without listing directory first"
else
    log_fail "Symlink not recognized as symlink without listing directory first"
fi

log_info "Directly reading symlink target from container 2 (no ls or cd first)..."
TARGET_DIRECT=$(run_in $MOUNT2 readlink $TEST_DIR/deep/nested/link.txt 2>/dev/null || echo "FAILED")
if [ "$TARGET_DIRECT" = "target.txt" ]; then
    log_pass "Symlink target correct without listing directory first"
else
    log_fail "Symlink target incorrect without listing directory (got: $TARGET_DIRECT)"
fi

cleanup_test_folder 7

# ==============================================================================
log_header "TEST 8: Rename symlink within same directory"
# ==============================================================================

setup_test_folder 8

log_info "Creating target file and symlink..."
run_in $MOUNT1 sh -c "echo 'Rename test content' > $TEST_DIR/target.txt"
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/oldname.txt
sync_and_wait

log_info "Verifying symlink before rename..."
CONTENT_BEFORE=$(run_in $MOUNT1 cat $TEST_DIR/oldname.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_BEFORE" = "Rename test content" ]; then
    log_pass "Symlink works before rename"
else
    log_fail "Symlink doesn't work before rename (got: $CONTENT_BEFORE)"
fi

log_info "Checking .geesefs_symlinks before rename..."
SYMLINKS_BEFORE=$(get_symlinks_file_s3 "test8")
echo "  Content before rename:"
echo "$SYMLINKS_BEFORE" | sed 's/^/    /'

log_info "Renaming symlink: oldname.txt -> newname.txt"
run_in $MOUNT1 mv $TEST_DIR/oldname.txt $TEST_DIR/newname.txt
sync_and_wait

log_info "Verifying renamed symlink works..."
CONTENT_AFTER=$(run_in $MOUNT1 cat $TEST_DIR/newname.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_AFTER" = "Rename test content" ]; then
    log_pass "Renamed symlink works in container 1"
else
    log_fail "Renamed symlink doesn't work in container 1 (got: $CONTENT_AFTER)"
fi

log_info "Verifying old name no longer exists..."
if run_in $MOUNT1 test -e $TEST_DIR/oldname.txt 2>/dev/null; then
    log_fail "Old symlink name still exists after rename"
else
    log_pass "Old symlink name removed after rename"
fi

log_info "Checking .geesefs_symlinks after rename..."
SYMLINKS_AFTER=$(get_symlinks_file_s3 "test8")
echo "  Content after rename:"
echo "$SYMLINKS_AFTER" | sed 's/^/    /'

if echo "$SYMLINKS_AFTER" | grep -q "newname.txt"; then
    log_pass ".geesefs_symlinks contains newname.txt"
else
    log_fail ".geesefs_symlinks missing newname.txt"
fi

if echo "$SYMLINKS_AFTER" | grep -q "oldname.txt"; then
    log_fail ".geesefs_symlinks still contains oldname.txt"
else
    log_pass ".geesefs_symlinks no longer contains oldname.txt"
fi

log_info "Waiting for cache refresh in container 2..."
sleep 5
run_in $MOUNT2 ls -la $TEST_DIR/ >/dev/null 2>&1 || true
sleep 2

log_info "Checking renamed symlink visibility in container 2..."
CONTENT_M2=$(run_in $MOUNT2 cat $TEST_DIR/newname.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M2" = "Rename test content" ]; then
    log_pass "Renamed symlink visible and works in container 2"
else
    log_fail "Renamed symlink not working in container 2 (got: $CONTENT_M2)"
fi

cleanup_test_folder 8

# ==============================================================================
log_header "TEST 9: Rename symlink to different directory"
# ==============================================================================

setup_test_folder 9

log_info "Creating source and destination directories..."
run_in $MOUNT1 mkdir -p $TEST_DIR/srcdir
run_in $MOUNT1 mkdir -p $TEST_DIR/dstdir
run_in $MOUNT1 sh -c "echo 'Cross-dir rename content' > $TEST_DIR/srcdir/target.txt"
run_in $MOUNT1 sh -c "echo 'Cross-dir rename content' > $TEST_DIR/dstdir/target.txt"
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/srcdir/link.txt
sync_and_wait

log_info "Verifying symlink in source dir before rename..."
CONTENT_SRC=$(run_in $MOUNT1 cat $TEST_DIR/srcdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_SRC" = "Cross-dir rename content" ]; then
    log_pass "Symlink works in source dir before rename"
else
    log_fail "Symlink doesn't work in source dir (got: $CONTENT_SRC)"
fi

log_info "Checking source .geesefs_symlinks before rename..."
SRC_SYMLINKS=$(get_symlinks_file_s3 "test9/srcdir")
echo "  Source before rename:"
echo "$SRC_SYMLINKS" | sed 's/^/    /'

log_info "Renaming symlink from srcdir to dstdir..."
run_in $MOUNT1 mv $TEST_DIR/srcdir/link.txt $TEST_DIR/dstdir/link.txt
sync_and_wait

log_info "Verifying symlink removed from source dir..."
if run_in $MOUNT1 test -e $TEST_DIR/srcdir/link.txt 2>/dev/null; then
    log_fail "Symlink still exists in source dir after rename"
else
    log_pass "Symlink removed from source dir after rename"
fi

log_info "Verifying symlink works in destination dir..."
CONTENT_DST=$(run_in $MOUNT1 cat $TEST_DIR/dstdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_DST" = "Cross-dir rename content" ]; then
    log_pass "Renamed symlink works in destination dir"
else
    log_fail "Renamed symlink doesn't work in destination dir (got: $CONTENT_DST)"
fi

log_info "Checking .geesefs_symlinks in source dir after rename..."
SRC_AFTER=$(get_symlinks_file_s3 "test9/srcdir")
if [ -z "$SRC_AFTER" ] || ! echo "$SRC_AFTER" | grep -q "link.txt"; then
    log_pass "Source .geesefs_symlinks no longer contains link.txt"
else
    log_fail "Source .geesefs_symlinks still contains link.txt"
    echo "  Content:"
    echo "$SRC_AFTER" | sed 's/^/    /'
fi

log_info "Checking .geesefs_symlinks in destination dir after rename..."
DST_AFTER=$(get_symlinks_file_s3 "test9/dstdir")
if echo "$DST_AFTER" | grep -q "link.txt"; then
    log_pass "Destination .geesefs_symlinks contains link.txt"
else
    log_fail "Destination .geesefs_symlinks missing link.txt"
fi
echo "  Content:"
echo "$DST_AFTER" | sed 's/^/    /'

log_info "Waiting for cache refresh in container 2..."
sleep 5
run_in $MOUNT2 ls -la $TEST_DIR/dstdir/ >/dev/null 2>&1 || true
sleep 2

log_info "Checking cross-dir renamed symlink visibility in container 2..."
CONTENT_M2=$(run_in $MOUNT2 cat $TEST_DIR/dstdir/link.txt 2>/dev/null || echo "FAILED")
if [ "$CONTENT_M2" = "Cross-dir rename content" ]; then
    log_pass "Cross-dir renamed symlink visible and works in container 2"
else
    log_fail "Cross-dir renamed symlink not working in container 2 (got: $CONTENT_M2)"
fi

cleanup_test_folder 9

# ==============================================================================
log_header "TEST 10: Batch create — rapid symlinks all visible after flush"
# ==============================================================================

setup_test_folder 10

log_info "Creating target files..."
for i in 1 2 3 4 5 6 7 8 9 10; do
    run_in $MOUNT1 sh -c "echo 'target$i content' > $TEST_DIR/target$i.txt"
done
sync_and_wait

log_info "Creating 10 symlinks in rapid succession..."
for i in 1 2 3 4 5 6 7 8 9 10; do
    run_in $MOUNT1 ln -sf target$i.txt $TEST_DIR/link$i.txt
done

log_info "Syncing to flush batched changes..."
run_in $MOUNT1 sync
sleep 3

log_info "Verifying all 10 symlinks in container 1..."
ALL_OK="yes"
for i in 1 2 3 4 5 6 7 8 9 10; do
    CONTENT=$(run_in $MOUNT1 cat $TEST_DIR/link$i.txt 2>/dev/null || echo "FAILED")
    if [ "$CONTENT" != "target$i content" ]; then
        log_fail "link$i.txt content incorrect (got: $CONTENT)"
        ALL_OK="no"
    fi
done
if [ "$ALL_OK" = "yes" ]; then
    log_pass "All 10 symlinks work correctly in container 1"
fi

log_info "Checking .geesefs_symlinks file via S3 API..."
SYMLINKS_CONTENT=$(get_symlinks_file_s3 "test10")
if [ -n "$SYMLINKS_CONTENT" ]; then
    LINK_COUNT=$(echo "$SYMLINKS_CONTENT" | grep -o '"link[0-9]*\.txt"' | wc -l)
    LINK_COUNT=$(echo "$LINK_COUNT" | tr -d ' ')
    if [ "$LINK_COUNT" = "10" ]; then
        log_pass ".geesefs_symlinks contains all 10 symlink entries"
    else
        log_fail ".geesefs_symlinks contains $LINK_COUNT entries (expected 10)"
        echo "  Content:"
        echo "$SYMLINKS_CONTENT" | sed 's/^/    /'
    fi
else
    log_fail ".geesefs_symlinks file not found in S3"
fi

log_info "Waiting for cache refresh in container 2..."
sleep 5
run_in $MOUNT2 ls -la $TEST_DIR/ >/dev/null 2>&1 || true
sleep 2

log_info "Verifying all 10 symlinks visible in container 2..."
ALL_OK="yes"
for i in 1 2 3 4 5 6 7 8 9 10; do
    CONTENT=$(run_in $MOUNT2 cat $TEST_DIR/link$i.txt 2>/dev/null || echo "FAILED")
    if [ "$CONTENT" != "target$i content" ]; then
        log_fail "link$i.txt not visible/correct in container 2 (got: $CONTENT)"
        ALL_OK="no"
    fi
done
if [ "$ALL_OK" = "yes" ]; then
    log_pass "All 10 symlinks visible and correct in container 2"
fi

cleanup_test_folder 10

# ==============================================================================
log_header "TEST 11: Fsync flush — create symlinks then sync forces S3 persistence"
# ==============================================================================

setup_test_folder 11

log_info "Creating target file..."
run_in $MOUNT1 sh -c "echo 'fsync test' > $TEST_DIR/target.txt"
sync_and_wait

log_info "Creating symlinks rapidly..."
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/fsync_link1.txt
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/fsync_link2.txt
run_in $MOUNT1 ln -sf target.txt $TEST_DIR/fsync_link3.txt

log_info "Calling sync to force S3 persistence..."
run_in $MOUNT1 sync
sleep 2

log_info "Verifying S3 has symlinks immediately after sync..."
SYMLINKS_CONTENT=$(get_symlinks_file_s3 "test11")
if [ -n "$SYMLINKS_CONTENT" ]; then
    FOUND_ALL="yes"
    for name in fsync_link1.txt fsync_link2.txt fsync_link3.txt; do
        if ! echo "$SYMLINKS_CONTENT" | grep -q "$name"; then
            log_fail ".geesefs_symlinks missing $name after sync"
            FOUND_ALL="no"
        fi
    done
    if [ "$FOUND_ALL" = "yes" ]; then
        log_pass "All symlinks persisted in S3 after sync"
    fi
else
    log_fail ".geesefs_symlinks not found in S3 after sync"
fi

cleanup_test_folder 11

# ==============================================================================
log_header "TEST SUMMARY"
# ==============================================================================

echo ""
printf "Passed: ${GREEN}%s${NC}\n" "$PASSED"
printf "Failed: ${RED}%s${NC}\n" "$FAILED"
echo ""

if [ $FAILED -eq 0 ]; then
    printf "${GREEN}All tests passed!${NC}\n"
    exit 0
else
    printf "${RED}Some tests failed!${NC}\n"
    exit 1
fi
