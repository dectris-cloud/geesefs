# GeeseFS Docker Tests

This folder contains Docker-based integration tests for GeeseFS.

## Prerequisites

- Docker and Docker Compose
- [just](https://github.com/casey/just) command runner (recommended)

## Test Structure

Each test suite has its own subfolder with isolated docker-compose setup:

```text
docker/
├── tests/
│   ├── conditional_writes/    # S3 If-Match/If-None-Match API tests
│   ├── geesefs_conditional/   # Conditional writes through FUSE mount
│   └── symlinks_file/         # .geesefs_symlinks cross-mount visibility
├── justfile                   # Main test runner
└── README.md
```

## Quick Start

```bash
cd docker

# Show all available commands
just

# Run all tests
just test-all

# Run specific tests
just test-conditional-writes    # S3 API level conditional writes
just test-geesefs-conditional   # FUSE level conditional writes
just test-symlinks              # Symlinks file tests

# Clean up all resources
just clean-all
```

## Individual Test Suites

### Conditional Writes Test (`tests/conditional_writes/`)

Tests S3 conditional writes (If-Match/If-None-Match) directly against MinIO using the S3 API.

```bash
cd tests/conditional_writes
just test   # Run test
just clean  # Cleanup
```

### GeeseFS Conditional Test (`tests/geesefs_conditional/`)

Tests conditional writes through the GeeseFS FUSE mount with two concurrent mounts.

```bash
cd tests/geesefs_conditional
just test    # Run test
just up      # Start services only
just shell-1 # Shell into mount 1
just shell-2 # Shell into mount 2
just logs    # View GeeseFS logs
just clean   # Cleanup
```

### Symlinks File Test (`tests/symlinks_file/`)

Tests the `.geesefs_symlinks` file feature for cross-mount symlink visibility.

```bash
cd tests/symlinks_file
just test        # Run test
just up          # Start services only
just shell-1     # Shell into mount 1
just shell-2     # Shell into mount 2
just logs        # View GeeseFS logs
just ls-symlinks # Show .geesefs_symlinks files
just console     # Open MinIO web console
just clean       # Cleanup
```

## Developing Tests

Each test folder contains:

- `docker-compose.yml` - Container orchestration
- `Dockerfile.*` - Container build files
- `justfile` - Test-specific commands
- `test_*.sh` or `test_*.py` - Test script
- `README.md` - Test documentation

To modify a test:

1. `cd` into the test folder
2. Run `just up` to start services
3. Make changes and run `just test`
4. Run `just clean` when done
