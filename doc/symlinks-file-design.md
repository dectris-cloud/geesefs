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

## Implementation Details

### CreateSymlink (with EnableSymlinksFile=true)

1. Update `.geesefs_symlinks` file (conditional PUT)
2. Insert inode with `ST_CACHED` state (no S3 object created—symlink is purely virtual)

### Unlink (symlink with EnableSymlinksFile=true)

1. Update `.geesefs_symlinks` file to remove entry
2. Remove inode from cache (no S3 deletion needed—symlink is purely virtual)

## Performance Considerations

| Operation | Without EnableSymlinksFile | With EnableSymlinksFile |
|-----------|---------------------------|------------------------|
| Create symlink | 1 PUT (object with metadata) | 1 PUT (`.geesefs_symlinks`) |
| Delete symlink | 1 DELETE | 1 PUT (`.geesefs_symlinks`) |
| List directory | N HEADs to find symlinks | 1 GET (`.geesefs_symlinks`) |

**Trade-off:** Lower write and delete cost (no per-symlink S3 objects). If `.geesefs_symlinks` is deleted, symlinks are not visible until manual recovery is performed.

## Configuration

```text
--enable-symlinks-file    Store symlinks in .geesefs_symlinks file (default: false)
--symlinks-file NAME      Name of symlinks file (default: .geesefs_symlinks)
```

## Future Considerations

1. **Batch operations:** Combine multiple symlink creates into single `.geesefs_symlinks` update
2. **Compression:** Compress `.geesefs_symlinks` file for directories with many symlinks
