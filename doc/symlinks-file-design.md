# Symlinks File Feature Design Document

## Overview

When `--enable-symlinks-file` is enabled, GeeseFS stores symlink metadata in a `.geesefs_symlinks` JSON file per directory. This enables cross-mount visibility of symlinks without requiring immediate cache invalidation.

## Problem Statement

When a symlink is created on one GeeseFS mount, other mounts need to be able to discover it. A naive approach that relies on per-object metadata would require each mount to HEAD every object, which is expensive.

## Solution: `.geesefs_symlinks` File

The `.geesefs_symlinks` file is the **authoritative source** for symlink information during normal operation.

```json
{
  "version": 1,
  "symlinks": {
    "link-name.txt": {
      "target": "target-file.txt",
      "mtime": 1706400000
    }
  }
}
```

**Benefits:**

- Single GET request retrieves all symlinks in a directory
- Conditional GET (If-None-Match) enables efficient cache validation
- Atomic updates via conditional PUT (If-Match)

**Visibility:**

The `.geesefs_symlinks` file is hidden from directory listings by default. Users only see the symlinks themselves, not the underlying metadata file.

## Behavior Matrix

*When `--enable-symlinks-file` is enabled:*

| `.geesefs_symlinks` has entry | Result |
|----------------------|--------|
| Yes | ✅ Symlink (from `.geesefs_symlinks` cache) |
| No | Regular file or nothing (symlink not recognized) |

## Directory Listing Flow

```text
1. loadListing() called
   │
2. loadSymlinksCache() called FIRST
   │
   ├─ If .geesefs_symlinks exists → Load into cache
   │
   └─ If .geesefs_symlinks missing → Empty cache
   │
3. ListBlobs returns objects
   │
4. insertSubTree processes each object
   │
   └─ Check symlinksCache for each name
      │
      ├─ If in cache → Apply symlink mode
      └─ If not in cache → Regular file
   │

```

## Edge Cases

### `.geesefs_symlinks` Deleted During Listing

If `.geesefs_symlinks` is deleted while a mount is mid-listing:

- Symlinks may temporarily appear missing until `.geesefs_symlinks` is restored
- No automatic rebuild occurs; `.geesefs_symlinks` must be recreated manually

**Mitigation:** Restore `.geesefs_symlinks` manually if needed.

### Manual Recovery (No Auto-Rebuild)

If `.geesefs_symlinks` is deleted, the system will not reconstruct it automatically. Operators must restore it by one of the following:

- Recreate `.geesefs_symlinks` from a backup, or
- Recreate the symlinks (which will repopulate `.geesefs_symlinks`), or
- Manually author a valid `.geesefs_symlinks` JSON and upload it.

### Race Condition: Last Symlink Deletion

When deleting the last symlink in a directory:

- Mount A deletes the last symlink → deletes `.geesefs_symlinks` entirely
- Mount B simultaneously creates a new symlink → conditional PUT fails (no file to match ETag)

**Resolution:** Mount B detects the missing file and creates a new `.geesefs_symlinks` with its symlink entry.

## Implementation Details

### CreateSymlink (with EnableSymlinksFile=true)

1. Update `.geesefs_symlinks` file (conditional PUT with exponential backoff retry on conflict)
2. Insert inode with `ST_CACHED` state (no S3 object created—symlink is purely virtual)

### Unlink (symlink with EnableSymlinksFile=true)

1. Update `.geesefs_symlinks` file to remove entry (conditional PUT with exponential backoff retry)
2. If this was the last symlink, delete `.geesefs_symlinks` file entirely
3. Remove inode from cache (no S3 deletion needed—symlink is purely virtual)

### Concurrency Handling

When multiple mounts attempt to update `.geesefs_symlinks` simultaneously:

1. Each mount uses conditional PUT with `If-Match` (ETag)
2. If ETag mismatch (concurrent modification), the operation fails
3. On failure: fetch latest `.geesefs_symlinks`, merge changes, retry with exponential backoff
4. Maximum retry attempts with increasing delays to prevent thundering herd

### lstat() Behavior

When `lstat()` is called on a symlink:

- **Size:** Length of the target path string
- **Mtime:** Value from `.geesefs_symlinks` metadata
- **Mode:** `S_IFLNK` (symlink) with appropriate permissions

Note: `lstat()` does not follow the symlink. Use `stat()` to get target file attributes.

## Performance Considerations

| Operation | Without EnableSymlinksFile | With EnableSymlinksFile |
|-----------|---------------------------|------------------------|
| Create symlink | 1 PUT (object with metadata) | 1 PUT (`.geesefs_symlinks`) |
| Delete symlink | 1 DELETE | 1 PUT (`.geesefs_symlinks`) |
| List directory | N HEADs to find symlinks | 1 GET (`.geesefs_symlinks`) |

**Trade-off:** Lower write and delete cost (no per-symlink S3 objects). If `.geesefs_symlinks` is deleted, symlinks are not visible until manual recovery is performed.

## Symlink Behavior

### Dangling Symlinks

Symlinks can point to non-existent targets (dangling symlinks). No validation is performed at creation time.

### Cross-Directory Targets

Symlinks can point to targets in other directories using relative paths:

```text
../other-dir/file.txt
../../parent/sibling/file.txt
```

The target path is stored as-is (relative). Symlinks can also point to paths outside the mounted bucket—resolution is handled by the kernel at access time.

### Symlink Chains (Not Supported)

Chained symlinks (symlink → symlink → file) are **not supported**. A symlink must point directly to a regular file or directory, not to another symlink.

## Configuration

```text
--enable-symlinks-file    Store symlinks in .geesefs_symlinks file (default: false)
--symlinks-file NAME      Name of symlinks file (default: .geesefs_symlinks)
```

## Future Considerations

1. **Batch operations:** Combine multiple symlink creates into single `.geesefs_symlinks` update
2. **Compression:** Compress `.geesefs_symlinks` file for directories with many symlinks
