#!/usr/bin/env python3
"""
Test script for S3 conditional writes (If-Match / If-None-Match).

This script demonstrates:
1. If-None-Match: "*" - Create only if object doesn't exist
2. If-Match: <etag> - Update only if ETag matches (optimistic locking)
3. Concurrent write conflict detection

These features were added to S3 in August 2024 and are useful for:
- Preventing race conditions when multiple writers access the same object
- Implementing optimistic locking patterns
- Ensuring atomic create-if-not-exists operations
"""

import os
import sys
import time
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed

import boto3
from botocore.config import Config
from botocore.exceptions import ClientError


# Configuration from environment
S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "http://minio:9000")
S3_BUCKET = os.environ.get("S3_BUCKET", "testbucket")
AWS_ACCESS_KEY_ID = os.environ.get("AWS_ACCESS_KEY_ID", "minioadmin")
AWS_SECRET_ACCESS_KEY = os.environ.get("AWS_SECRET_ACCESS_KEY", "minioadmin")


def get_s3_client():
    """Create an S3 client configured for MinIO."""
    return boto3.client(
        "s3",
        endpoint_url=S3_ENDPOINT,
        aws_access_key_id=AWS_ACCESS_KEY_ID,
        aws_secret_access_key=AWS_SECRET_ACCESS_KEY,
        config=Config(signature_version="s3v4"),
        region_name="us-east-1",
    )


def cleanup_test_objects(s3, prefix="test-"):
    """Clean up test objects from previous runs."""
    try:
        response = s3.list_objects_v2(Bucket=S3_BUCKET, Prefix=prefix)
        if "Contents" in response:
            for obj in response["Contents"]:
                s3.delete_object(Bucket=S3_BUCKET, Key=obj["Key"])
                print(f"  Deleted: {obj['Key']}")
    except ClientError as e:
        print(f"  Cleanup warning: {e}")


def test_if_none_match_create_new(s3):
    """
    Test If-None-Match: "*" - should succeed when object doesn't exist.
    """
    print("\n" + "=" * 60)
    print("TEST 1: If-None-Match='*' - Create new object")
    print("=" * 60)

    key = "test-if-none-match-new"

    # Ensure object doesn't exist
    try:
        s3.delete_object(Bucket=S3_BUCKET, Key=key)
    except:
        pass

    try:
        # This should succeed - object doesn't exist
        s3.put_object(
            Bucket=S3_BUCKET,
            Key=key,
            Body=b"Created with If-None-Match",
            IfNoneMatch="*"
        )
        print("✓ SUCCESS: Object created with If-None-Match='*'")

        # Verify
        response = s3.get_object(Bucket=S3_BUCKET, Key=key)
        content = response["Body"].read().decode()
        print(f"  Content: {content}")
        print(f"  ETag: {response['ETag']}")
        return True
    except ClientError as e:
        error_code = e.response["Error"]["Code"]
        print(f"✗ FAILED: {error_code} - {e.response['Error']['Message']}")
        return False


def test_if_none_match_exists(s3):
    """
    Test If-None-Match: "*" - should fail when object already exists.
    """
    print("\n" + "=" * 60)
    print("TEST 2: If-None-Match='*' - Fail if object exists")
    print("=" * 60)

    key = "test-if-none-match-exists"

    # Create the object first
    s3.put_object(Bucket=S3_BUCKET, Key=key, Body=b"Original content")
    print("  Pre-created object with content: 'Original content'")

    try:
        # This should FAIL - object already exists
        s3.put_object(
            Bucket=S3_BUCKET,
            Key=key,
            Body=b"This should not be written",
            IfNoneMatch="*"
        )
        print("✗ UNEXPECTED: Write succeeded when it should have failed!")
        return False
    except ClientError as e:
        error_code = e.response["Error"]["Code"]
        if error_code == "PreconditionFailed":
            print("✓ SUCCESS: Got expected PreconditionFailed error")
            # Verify original content is unchanged
            response = s3.get_object(Bucket=S3_BUCKET, Key=key)
            content = response["Body"].read().decode()
            print(f"  Original content preserved: '{content}'")
            return True
        else:
            print(f"✗ FAILED: Got unexpected error: {error_code}")
            return False


def test_if_match_correct_etag(s3):
    """
    Test If-Match with correct ETag - should succeed.
    """
    print("\n" + "=" * 60)
    print("TEST 3: If-Match with correct ETag - Update succeeds")
    print("=" * 60)

    key = "test-if-match-correct"

    # Create initial object and get its ETag
    response = s3.put_object(Bucket=S3_BUCKET, Key=key, Body=b"Version 1")
    etag = response["ETag"]
    print(f"  Created object with ETag: {etag}")

    try:
        # Update with correct ETag - should succeed
        new_response = s3.put_object(
            Bucket=S3_BUCKET,
            Key=key,
            Body=b"Version 2 - Updated with If-Match",
            IfMatch=etag
        )
        print("✓ SUCCESS: Object updated with correct ETag")
        print(f"  New ETag: {new_response['ETag']}")

        # Verify new content
        get_response = s3.get_object(Bucket=S3_BUCKET, Key=key)
        content = get_response["Body"].read().decode()
        print(f"  New content: '{content}'")
        return True
    except ClientError as e:
        error_code = e.response["Error"]["Code"]
        print(f"✗ FAILED: {error_code} - {e.response['Error']['Message']}")
        return False


def test_if_match_wrong_etag(s3):
    """
    Test If-Match with wrong ETag - should fail.
    """
    print("\n" + "=" * 60)
    print("TEST 4: If-Match with wrong ETag - Update fails")
    print("=" * 60)

    key = "test-if-match-wrong"

    # Create initial object
    s3.put_object(Bucket=S3_BUCKET, Key=key, Body=b"Original content")
    print("  Created object with content: 'Original content'")

    wrong_etag = '"wrongetag12345"'
    print(f"  Attempting update with wrong ETag: {wrong_etag}")

    try:
        # Update with wrong ETag - should FAIL
        s3.put_object(
            Bucket=S3_BUCKET,
            Key=key,
            Body=b"This should not be written",
            IfMatch=wrong_etag
        )
        print("✗ UNEXPECTED: Write succeeded when it should have failed!")
        return False
    except ClientError as e:
        error_code = e.response["Error"]["Code"]
        if error_code == "PreconditionFailed":
            print("✓ SUCCESS: Got expected PreconditionFailed error")
            # Verify original content is unchanged
            response = s3.get_object(Bucket=S3_BUCKET, Key=key)
            content = response["Body"].read().decode()
            print(f"  Original content preserved: '{content}'")
            return True
        else:
            print(f"✗ FAILED: Got unexpected error: {error_code}")
            return False


def test_optimistic_locking_race(s3):
    """
    Test optimistic locking with concurrent writers.
    Two threads try to update the same object - only one should succeed.
    """
    print("\n" + "=" * 60)
    print("TEST 5: Optimistic Locking - Concurrent Write Race")
    print("=" * 60)

    key = "test-race-condition"

    # Create initial object
    response = s3.put_object(Bucket=S3_BUCKET, Key=key, Body=b"Initial value: 0")
    initial_etag = response["ETag"]
    print(f"  Created object with ETag: {initial_etag}")

    results = {"success": 0, "failed": 0, "errors": []}
    lock = threading.Lock()

    def concurrent_writer(writer_id, etag):
        """Attempt to write using the given ETag."""
        client = get_s3_client()  # Each thread needs its own client
        try:
            # Simulate some processing time
            time.sleep(0.1)

            client.put_object(
                Bucket=S3_BUCKET,
                Key=key,
                Body=f"Written by writer-{writer_id}".encode(),
                IfMatch=etag
            )
            with lock:
                results["success"] += 1
            return f"Writer-{writer_id}: SUCCESS"
        except ClientError as e:
            error_code = e.response["Error"]["Code"]
            with lock:
                results["failed"] += 1
                results["errors"].append(error_code)
            return f"Writer-{writer_id}: FAILED ({error_code})"

    # Launch concurrent writers with the SAME ETag
    print("\n  Launching 5 concurrent writers with the same ETag...")
    with ThreadPoolExecutor(max_workers=5) as executor:
        futures = [
            executor.submit(concurrent_writer, i, initial_etag)
            for i in range(5)
        ]

        for future in as_completed(futures):
            print(f"  {future.result()}")

    print(f"\n  Results: {results['success']} succeeded, {results['failed']} failed")

    # Verify final state
    response = s3.get_object(Bucket=S3_BUCKET, Key=key)
    content = response["Body"].read().decode()
    print(f"  Final content: '{content}'")

    # Expected: exactly 1 success, rest should fail with PreconditionFailed
    if results["success"] == 1 and results["failed"] == 4:
        print("✓ SUCCESS: Exactly one writer won the race!")
        return True
    elif results["success"] > 1:
        print("✗ FAILED: Multiple writers succeeded - race condition!")
        return False
    else:
        print(f"? PARTIAL: Unexpected results - {results}")
        return results["success"] >= 1


def test_read_modify_write_pattern(s3):
    """
    Test the read-modify-write pattern with retry on conflict.
    """
    print("\n" + "=" * 60)
    print("TEST 6: Read-Modify-Write Pattern with Retry")
    print("=" * 60)

    key = "test-counter"

    # Initialize counter
    s3.put_object(Bucket=S3_BUCKET, Key=key, Body=b"0")
    print("  Initialized counter to 0")

    def increment_counter(client, incrementer_id):
        """Increment the counter with optimistic locking."""
        max_retries = 10
        for attempt in range(max_retries):
            # Read current value and ETag
            response = client.get_object(Bucket=S3_BUCKET, Key=key)
            current_value = int(response["Body"].read().decode())
            etag = response["ETag"]

            # Increment
            new_value = current_value + 1

            try:
                # Write with If-Match
                client.put_object(
                    Bucket=S3_BUCKET,
                    Key=key,
                    Body=str(new_value).encode(),
                    IfMatch=etag
                )
                return f"Incrementer-{incrementer_id}: {current_value} -> {new_value} (attempt {attempt + 1})"
            except ClientError as e:
                if e.response["Error"]["Code"] == "PreconditionFailed":
                    # Retry
                    continue
                raise

        return f"Incrementer-{incrementer_id}: FAILED after {max_retries} retries"

    # Run concurrent incrementers
    print("\n  Running 10 concurrent increment operations...")
    with ThreadPoolExecutor(max_workers=10) as executor:
        futures = [
            executor.submit(increment_counter, get_s3_client(), i)
            for i in range(10)
        ]

        for future in as_completed(futures):
            print(f"  {future.result()}")

    # Verify final counter value
    response = s3.get_object(Bucket=S3_BUCKET, Key=key)
    final_value = int(response["Body"].read().decode())
    print(f"\n  Final counter value: {final_value}")

    if final_value == 10:
        print("✓ SUCCESS: All increments applied correctly!")
        return True
    else:
        print(f"✗ FAILED: Expected 10, got {final_value}")
        return False


def main():
    print("=" * 60)
    print("S3 CONDITIONAL WRITE TESTS")
    print("=" * 60)
    print(f"Endpoint: {S3_ENDPOINT}")
    print(f"Bucket: {S3_BUCKET}")

    s3 = get_s3_client()

    # Wait for MinIO to be ready
    print("\nWaiting for S3 to be ready...")
    for i in range(30):
        try:
            s3.head_bucket(Bucket=S3_BUCKET)
            print("S3 is ready!")
            break
        except:
            time.sleep(1)
    else:
        print("ERROR: S3 not ready after 30 seconds")
        sys.exit(1)

    # Clean up from previous runs
    print("\nCleaning up previous test objects...")
    cleanup_test_objects(s3)

    # Run tests
    results = []

    results.append(("If-None-Match create new", test_if_none_match_create_new(s3)))
    results.append(("If-None-Match exists", test_if_none_match_exists(s3)))
    results.append(("If-Match correct ETag", test_if_match_correct_etag(s3)))
    results.append(("If-Match wrong ETag", test_if_match_wrong_etag(s3)))
    results.append(("Optimistic locking race", test_optimistic_locking_race(s3)))
    results.append(("Read-modify-write pattern", test_read_modify_write_pattern(s3)))

    # Summary
    print("\n" + "=" * 60)
    print("TEST SUMMARY")
    print("=" * 60)

    passed = sum(1 for _, r in results if r)
    failed = len(results) - passed

    for name, result in results:
        status = "✓ PASS" if result else "✗ FAIL"
        print(f"  {status}: {name}")

    print(f"\nTotal: {passed}/{len(results)} passed")

    if failed > 0:
        print("\n⚠️  Some tests failed. This may indicate:")
        print("   - MinIO doesn't support conditional writes (If-Match/If-None-Match)")
        print("   - The S3 backend implementation needs adjustment")
        sys.exit(1)
    else:
        print("\n✓ All tests passed!")
        sys.exit(0)


if __name__ == "__main__":
    main()
