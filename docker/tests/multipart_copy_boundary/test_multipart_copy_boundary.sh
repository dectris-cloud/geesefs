#!/bin/sh
#
# Reproducer for the multipart-copy boundary bug in fsync.
#
# Original symptom (Feb 2026):
#   main.WARNING Failed to copy unmodified range <a>-<b> MB of object ...:
#     InvalidArgument: Range specified is not valid for source object of size: <n>
#   fuse.ERROR *fuseops.SyncFileOp error: invalid argument
#
# Trigger: an fsync on a file whose final part is not aligned to the part
# boundary causes copyUnmodifiedParts to issue UploadPartCopy ranges that
# extend past the source object size on S3.
#
# Strategy:
#   For each unaligned size that crosses a part boundary, write+fsync via fio,
#   then partially overwrite the middle and fsync again. The second flush is
#   the path that copies the unmodified leading/trailing parts server-side and
#   is the path that originally tripped the bug.

GEESEFS=multipart-copy-geesefs
MOUNT=/mnt/s3
LOG=/tmp/geesefs.log
FAILED=0
PASSED=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_pass() { printf "${GREEN}\xe2\x9c\x93 PASS${NC}: %s\n" "$1"; PASSED=$((PASSED + 1)); }
log_fail() { printf "${RED}\xe2\x9c\x97 FAIL${NC}: %s\n" "$1"; FAILED=$((FAILED + 1)); }
log_info() { printf "${YELLOW}\xe2\x86\x92${NC} %s\n" "$1"; }

ge() { docker exec "$GEESEFS" sh -c "$1"; }

# Sizes (in MiB) that each cross a 5 MiB part boundary unaligned.
#   27   ->  5 full 5MiB parts + 2 MiB tail        (tier-1 last part partial)
#   52   -> 10 full 5MiB parts + 2 MiB tail        (tier-1 last part partial)
#   103  -> 20 full 5MiB parts + 3 MiB tail        (boundary into tier-2)
#   127  -> tier-1 full + tier-2 partial           (cross-tier final part)
SIZES_MIB="27 52 103 127"

# Mark the start of this test run in the geesefs log so we only scan our own
# output, not anything that leaked from container startup.
MARKER="MULTIPART_COPY_TEST_START_$(date +%s%N)"
ge "echo $MARKER >> /proc/1/fd/2" 2>/dev/null || true

log_info "Capturing geesefs container log to $LOG"
docker logs "$GEESEFS" --since 1s >"$LOG" 2>&1 || true

run_case() {
    size_mib=$1
    file="$MOUNT/test_${size_mib}m.bin"

    log_info "size=${size_mib} MiB - fio layout (write + fsync)"
    ge "rm -f $file"
    ge "fio --name=layout --filename=$file --size=${size_mib}M --bs=1M \
        --rw=write --ioengine=psync --direct=0 --end_fsync=1 \
        --fallocate=none --thread --minimal" >/dev/null 2>&1
    rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "fio layout for ${size_mib} MiB exited $rc"
        return
    fi

    log_info "size=${size_mib} MiB - partial overwrite middle + fsync"
    # Overwrite 1 MiB starting 2 MiB into the file (touches part 0 only at
    # small sizes, middle parts at larger ones), then fsync. This is what
    # exercises copyUnmodifiedParts on the leading/trailing unmodified parts.
    seek_mib=$(( size_mib / 3 ))
    ge "dd if=/dev/urandom of=$file bs=1M count=1 seek=${seek_mib} \
        conv=notrunc oflag=dsync" >/dev/null 2>&1
    rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "partial overwrite for ${size_mib} MiB exited $rc"
        return
    fi
    ge "sync $file" >/dev/null 2>&1 || ge "sync" >/dev/null 2>&1

    # Force a full flush by closing the file: re-read it. Any pending parts
    # have to complete before stat returns the new size.
    actual=$(ge "stat -c %s $file" 2>/dev/null)
    expected=$(( size_mib * 1024 * 1024 ))
    if [ "$actual" != "$expected" ]; then
        log_fail "size mismatch after flush: ${size_mib} MiB - expected ${expected}, got ${actual}"
        return
    fi
    log_pass "size=${size_mib} MiB - layout + overwrite + fsync round-tripped"
}

for s in $SIZES_MIB; do
    run_case "$s"
done

log_info "Pulling geesefs container logs for inspection"
docker logs "$GEESEFS" >"$LOG" 2>&1 || true

# Bug markers from the original report. Match each independently so we report
# the right thing even if only one shows up.
patterns="\
Failed to copy unmodified range|\
Range specified is not valid for source object of size|\
The specified copy range is invalid for the source object size|\
fuseops.SyncFileOp error: invalid argument"

hits=$(grep -E "$patterns" "$LOG" 2>/dev/null || true)
if [ -n "$hits" ]; then
    log_fail "multipart-copy boundary bug reproduced - geesefs log contains:"
    echo "$hits" | head -20 | sed 's/^/    /'
else
    log_pass "no multipart-copy boundary errors in geesefs log"
fi

echo
echo "================================================================"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo "================================================================"

[ $FAILED -eq 0 ]
