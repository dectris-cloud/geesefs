#!/bin/sh
#
# Reproducer for the multipart-copy boundary bug in fsync.
#
# Original symptom (Feb 2026):
#   main.WARNING Failed to copy unmodified range <a>-<b> MB of object ...:
#     InvalidArgument: Range specified is not valid for source object of size: <n>
#   fuse.ERROR *fuseops.SyncFileOp error: invalid argument
#
# Trigger: completeMultipart -> copyUnmodifiedParts issues UploadPartCopy
# ranges whose part-aligned end exceeds the source object size on S3. The
# bug shows up when geesefs has flushed an earlier version of the object
# to S3 at size X, then modifies the file locally and fsyncs again with a
# new logical size > X (or with a part-aligned end > X for some unmodified
# range), and S3 rejects the copy as "Range specified is not valid".
#
# Reproduction strategy:
#   1. Run geesefs under tight RAM (compose mem_limit + --memory-limit) and
#      small part sizes so multi-flush states arise quickly with small files.
#   2. Pattern A: write+fsync at unaligned sizes.
#   3. Pattern B: write+fsync, RESTART geesefs (forces fresh open from S3),
#      partial-overwrite middle, fsync. The second fsync is the path that
#      UploadPartCopies from the existing S3 object.
#   4. Grep the geesefs log for the bug markers.

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

geesefs_alive() {
    state=$(docker inspect -f '{{.State.Status}}' "$GEESEFS" 2>/dev/null)
    [ "$state" = "running" ]
}

abort_if_dead() {
    if ! geesefs_alive; then
        oom=$(docker inspect -f '{{.State.OOMKilled}}' "$GEESEFS" 2>/dev/null)
        exit_code=$(docker inspect -f '{{.State.ExitCode}}' "$GEESEFS" 2>/dev/null)
        log_fail "geesefs container died (OOMKilled=$oom ExitCode=$exit_code) - aborting"
        return 1
    fi
    return 0
}

wait_for_mount() {
    for _ in $(seq 1 30); do
        if docker exec "$GEESEFS" mountpoint -q "$MOUNT" 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}

restart_geesefs() {
    log_info "Restarting geesefs container (clears local cache, forces S3 re-open)"
    docker restart "$GEESEFS" >/dev/null 2>&1
    if ! wait_for_mount; then
        log_fail "geesefs mount did not come back after restart"
        return 1
    fi
    sleep 2
    return 0
}

# Unaligned sizes that cross part boundaries.
#   100 MiB -> fully inside tier-1 (20 x 5 MB), with the final part on the boundary
#   147 MiB -> tier-1 full + 2 tier-2 parts partial (147 = 100 + 47, tier-2 part = 25)
#   223 MiB -> tier-1 full + tier-2 4 full + tier-2 partial (223 = 100 + 123)
SIZES_MIB="100 147 223"

run_pattern_a() {
    size_mib=$1
    file="$MOUNT/A_${size_mib}m.bin"

    log_info "[A] size=${size_mib} MiB - fio layout under RAM pressure"
    ge "rm -f $file"
    ge "fio --name=layout --filename=$file --size=${size_mib}M --bs=1M \
        --rw=write --ioengine=psync --direct=0 --end_fsync=1 \
        --fallocate=none --thread --minimal" >/dev/null 2>&1
    rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "[A] fio layout for ${size_mib} MiB exited $rc"
        return
    fi

    actual=$(ge "stat -c %s $file" 2>/dev/null)
    expected=$(( size_mib * 1024 * 1024 ))
    if [ "$actual" != "$expected" ]; then
        log_fail "[A] size mismatch ${size_mib} MiB - expected ${expected}, got ${actual}"
        return
    fi
    log_pass "[A] size=${size_mib} MiB - write+fsync under RAM pressure"
}

run_pattern_b() {
    size_mib=$1
    file="$MOUNT/B_${size_mib}m.bin"

    log_info "[B] size=${size_mib} MiB - initial write+fsync"
    ge "rm -f $file"
    ge "fio --name=layout --filename=$file --size=${size_mib}M --bs=1M \
        --rw=write --ioengine=psync --direct=0 --end_fsync=1 \
        --fallocate=none --thread --minimal" >/dev/null 2>&1
    rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "[B] initial fio for ${size_mib} MiB exited $rc"
        return
    fi

    restart_geesefs || return

    seek_mib=$(( size_mib / 2 ))
    log_info "[B] size=${size_mib} MiB - overwrite 1 MiB at offset ${seek_mib} MiB, fsync"
    ge "dd if=/dev/urandom of=$file bs=1M count=1 seek=${seek_mib} \
        conv=notrunc oflag=dsync" >/dev/null 2>&1
    rc=$?
    if [ $rc -ne 0 ]; then
        log_fail "[B] partial overwrite ${size_mib} MiB exited $rc"
        return
    fi
    ge "sync" >/dev/null 2>&1

    actual=$(ge "stat -c %s $file" 2>/dev/null)
    expected=$(( size_mib * 1024 * 1024 ))
    if [ "$actual" != "$expected" ]; then
        log_fail "[B] size mismatch after re-flush ${size_mib} MiB - expected ${expected}, got ${actual}"
        return
    fi
    log_pass "[B] size=${size_mib} MiB - re-open + middle overwrite + fsync"
}

for s in $SIZES_MIB; do
    abort_if_dead || break
    run_pattern_a "$s"
done

for s in $SIZES_MIB; do
    abort_if_dead || break
    run_pattern_b "$s"
done

log_info "Pulling geesefs container logs"
docker logs "$GEESEFS" >"$LOG" 2>&1 || true

# Bug markers from the original report.
patterns="\
Failed to copy unmodified range|\
Range specified is not valid for source object of size|\
The specified copy range is invalid for the source object size|\
fuseops.SyncFileOp error: invalid argument"

hits=$(grep -E "$patterns" "$LOG" 2>/dev/null || true)
if [ -n "$hits" ]; then
    log_fail "multipart-copy boundary bug reproduced - geesefs log contains:"
    echo "$hits" | head -30 | sed 's/^/    /'
else
    log_pass "no multipart-copy boundary errors in geesefs log"
fi

echo
echo "================================================================"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo "================================================================"

[ $FAILED -eq 0 ]
