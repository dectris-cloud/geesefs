# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GeeseFS is a high-performance FUSE filesystem for S3 (and Azure/GCS) written in Go. It mounts S3 buckets as local filesystems with aggressive parallelism and async I/O. This is a Dectris fork adding symlinks-file support.

## Build & Run

```bash
# Build
go build -ldflags "-X main.Version=$(git rev-parse HEAD)"

# Build (static, no CGO)
CGO_ENABLED=0 go build -ldflags "-X main.Version=$(git rev-parse HEAD)"

# Docker build (produces static Linux binary)
make docker-binary
```

## Testing

**Test framework**: `gopkg.in/check.v1` (not stdlib `testing`). Test functions use `(t *C)` not `(t *testing.T)`.

```bash
# Run all unit tests in core/
go test ./core/ -v -check.vv

# Run a specific test suite (e.g., SymlinksTest)
go test ./core/ -check.f "Symlinks"

# Run a specific test by name substring
go test ./core/ -check.f "DeepCopy"

# Run docker integration tests (requires Docker, uses MinIO)
cd docker && just test-all

# Run individual docker test suites
cd docker && just test-symlinks
cd docker && just test-conditional-writes
cd docker && just test-geesefs-conditional
cd docker && just test-multipart-boundary
```

**Note**: `go test -run "TestName"` won't work for check.v1 tests — use `-check.f` instead. The `BackendTest` suite requires a local S3 at `127.0.0.1:8080`.

## Architecture

**Entry point**: `main.go` — CLI parsing, daemonization, FUSE mount setup.

**Core package** (`core/`):
- `goofys.go` — Main `Goofys` struct, filesystem initialization, inode table
- `handles.go` — `Inode` struct definition, file handle management
- `dir.go` — Directory operations (lookup, readdir, create, rename, unlink), symlinks cache integration
- `file.go` — File read/write, buffering, multipart uploads
- `symlinks.go` — `.geesefs_symlinks` JSON file format, S3 conditional read/write operations
- `backend*.go` — `StorageBackend` interface implementations (S3, Azure, GCS)
- `cfg/` — `FlagStorage` configuration, CLI flag definitions

**Key structs**:
- `Goofys` — Filesystem root, inode table, background flusher
- `Inode` — File/directory node with metadata, buffers, state machine
- `DirInodeData` — Directory-specific: children list, symlinks cache, listing state

## Critical Patterns

**Locking**: The codebase uses careful mutex ordering documented with `LOCKS_REQUIRED`, `LOCKS_EXCLUDED`, `GUARDED_BY` annotations.
- `Inode.mu` — Per-inode mutex. Must be acquired before `Goofys.mu`.
- `parent.mu` — Directory mutex. **Must be released before S3 I/O** (network calls), then re-acquired after. See `loadSymlinksCache()` and `updateSymlinksFile()` for examples.
- Pattern: capture needed fields under lock → unlock → do I/O → re-lock → apply results.

**Inode states** (atomic int32): `ST_CACHED` (0), `ST_DEAD` (1), `ST_CREATED` (2), `ST_MODIFIED` (3), `ST_DELETED` (4).

**Cache TTL**: Use `expired(timestamp, fs.flags.StatCacheTTL)` for cache validity checks.

**S3 conditional writes**: `.geesefs_symlinks` uses `If-Match`/`If-None-Match` ETags for optimistic locking with `SaveSymlinksFileWithRetry` for conflict resolution. Set them via `PutBlobInput.IfMatch` / `.IfNoneMatch`; `applyS3PutConditions` in `backend_s3.go` writes them onto the request as raw headers, because the stock SDK's `PutObjectInput` has no such fields (only `GetObjectInput` does). Losing `If-Match` silently disables the retry-and-merge path, since it is the 412 that drives it — `docker/tests/symlinks_file` TEST 12 guards this.

**AWS error handling**: Use `awserr.RequestFailure` status codes (404, 304, 412), not string matching. See `isNotExist()`, `isNotModified()`, `isPreconditionFailed()` in `symlinks.go`.

**Virtual symlinks**: Symlinks stored in `.geesefs_symlinks` (no S3 object) are distinguished from S3-backed symlinks via `Inode.isVirtualSymlink` bool field. Always use this field for detection, not `userMetadata[SymlinkAttr] != nil`.

## Branches

- `dev` — the dectris release line. All fork work lands here, and releases are cut from it.
- `master` — mirror of `yandex-cloud/geesefs`. Sync upstream here, then merge `master` into `dev`.

Landing fork work on `master` means it is missing from `dev`, and a release cut afterwards silently ships without it.

## Releasing

The version lives in one place: `GEESEFS_VERSION` in `core/cfg/flags.go`. Bump it, merge to `dev`, then dispatch:

```bash
gh workflow run dectris-release.yml -f version=<version> --ref dev
gh release edit v<version> --prerelease=false --latest
```

The version is a dispatch parameter, so do not edit the workflow to change it. It has no default on purpose: a default duplicates `GEESEFS_VERSION` and goes stale the moment that constant moves.

A dispatched run is marked prerelease, hence the `release edit`. Pushing a `v*` tag instead publishes a full release directly, but only the dispatch path is exercised regularly.

Tag scheme: `v<upstream-version>-dc.<n>`, e.g. `v0.43.8-dc.2`.

## Go Module

Module: `github.com/yandex-cloud/geesefs` (Go 1.25). Uses the stock `github.com/aws/aws-sdk-go` with no `replace` directive. Yandex-only S3 extensions (`PatchObject`, `ListObjectsV1Ext`) live in `core/ycs3ext/`, built on the SDK's `request.Request` rather than a forked SDK.

## Downstream

`compute-amis` pins the version in `terraform/infrastructure/main.tf` (`geesefs_version`) and its AMI recipe downloads the release asset. A new release needs a matching bump there.
