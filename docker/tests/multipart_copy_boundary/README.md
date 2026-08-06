# Multipart-copy boundary reproducer

Reproduces (and asserts the absence of) the Feb 2026 fsync bug:

```text
main.WARNING Failed to copy unmodified range 21475-21500 MB of object ...:
  InvalidArgument: Range specified is not valid for source object of size: 22527606784
fuse.ERROR *fuseops.SyncFileOp error: invalid argument
```

## Trigger

`completeMultipart` calls `copyUnmodifiedParts(numParts, finalSize)`. When the
final part is not aligned to the part boundary, the part-aligned end of a
copy range can extend past the source object's size on S3, and S3 rejects the
`UploadPartCopy` with `InvalidArgument: Range specified is not valid`.

Originally observed against a ~21 GB file with default part sizes
(`5:1000,25:1000,125:8000`). This test shrinks the part sizes to `5:20,25:10`
so the same unaligned-final-part code path can be hit in seconds with files
of ~27/52/103/127 MiB.

## What the test does

1. Starts MinIO + GeeseFS with `--part-sizes 5:20,25:10 --debug_s3 --debug_fuse`.
2. For each unaligned size, runs `fio` to write+fsync the file, then a partial
   `dd` overwrite of the middle followed by `sync`. The second flush exercises
   `copyUnmodifiedParts` on the unmodified leading/trailing parts.
3. Verifies the post-flush file size matches the written size.
4. Greps the geesefs container log for any of:
   - `Failed to copy unmodified range`
   - `Range specified is not valid for source object of size`
   - `The specified copy range is invalid for the source object size`
   - `fuseops.SyncFileOp error: invalid argument`

A clean log + correct sizes = `PASS`. Any of the bug markers in the log = `FAIL`.

## Usage

```bash
cd docker
just test-multipart-boundary
```

or

```bash
cd docker/tests/multipart_copy_boundary
just test
```
