// Copyright 2026 Dectris Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/yandex-cloud/geesefs/core/cfg"
	. "gopkg.in/check.v1"
)

type SymlinksTest struct{}

var _ = Suite(&SymlinksTest{})

// ============================================================================
// Tests for SymlinksFileData struct operations
// ============================================================================

func (s *SymlinksTest) TestNewSymlinksFileData(t *C) {
	data := NewSymlinksFileData()
	t.Assert(data.Version, Equals, 1)
	t.Assert(len(data.Symlinks), Equals, 0)
	t.Assert(data.IsEmpty(), Equals, true)
}

func (s *SymlinksTest) TestAddSymlink(t *C) {
	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	t.Assert(data.IsEmpty(), Equals, false)
	t.Assert(data.HasSymlink("link1"), Equals, true)
	t.Assert(data.HasSymlink("link2"), Equals, false)

	target, ok := data.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../target1")
}

func (s *SymlinksTest) TestRemoveSymlink(t *C) {
	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")
	data.AddSymlink("link2", "/absolute/path")

	t.Assert(len(data.Symlinks), Equals, 2)

	data.RemoveSymlink("link1")
	t.Assert(len(data.Symlinks), Equals, 1)
	t.Assert(data.HasSymlink("link1"), Equals, false)
	t.Assert(data.HasSymlink("link2"), Equals, true)

	data.RemoveSymlink("link2")
	t.Assert(data.IsEmpty(), Equals, true)
}

func (s *SymlinksTest) TestSerializeAndParse(t *C) {
	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")
	data.AddSymlink("link2", "/absolute/path")

	bytes, err := data.Serialize()
	t.Assert(err, IsNil)
	t.Assert(len(bytes) > 0, Equals, true)

	parsed, err := ParseSymlinksFile(bytes)
	t.Assert(err, IsNil)
	t.Assert(parsed.Version, Equals, 1)
	t.Assert(len(parsed.Symlinks), Equals, 2)

	target1, ok := parsed.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target1, Equals, "../target1")

	target2, ok := parsed.GetSymlink("link2")
	t.Assert(ok, Equals, true)
	t.Assert(target2, Equals, "/absolute/path")
}

func (s *SymlinksTest) TestParseEmptyData(t *C) {
	data, err := ParseSymlinksFile([]byte{})
	t.Assert(err, IsNil)
	t.Assert(data.IsEmpty(), Equals, true)
}

func (s *SymlinksTest) TestParseInvalidJSON(t *C) {
	_, err := ParseSymlinksFile([]byte("not valid json"))
	t.Assert(err, NotNil)
}

func (s *SymlinksTest) TestLoadCorruptedSymlinksFile(t *C) {
	mock := newMockConditionalBackend()

	// Store corrupted JSON in S3
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte("<<<corrupted data>>>"),
		etag: "\"corrupt\"",
	}

	// LoadSymlinksFile should return an error for corrupted data
	_, _, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, NotNil)
}

func (s *SymlinksTest) TestRecoveryAfterCorruption(t *C) {
	mock := newMockConditionalBackend()

	// Store corrupted JSON in S3
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte("{invalid json"),
		etag: "\"corrupt\"",
	}

	// Load fails due to corruption
	_, _, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, NotNil)

	// Overwrite with valid data (using the known ETag)
	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")
	newETag, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "\"corrupt\"")
	t.Assert(err, IsNil)
	t.Assert(newETag != "", Equals, true)

	// Load should now succeed
	loaded, _, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)
	target, ok := loaded.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../target1")
}

func (s *SymlinksTest) TestParseMissingSymlinksField(t *C) {
	// Valid JSON but missing "symlinks" field
	data, err := ParseSymlinksFile([]byte(`{"version":1}`))
	t.Assert(err, IsNil)
	t.Assert(data.IsEmpty(), Equals, true)
	t.Assert(data.Symlinks, NotNil) // should be initialized, not nil
}

func (s *SymlinksTest) TestParseUnknownVersion(t *C) {
	// Higher version number should still parse (forward compatible)
	data, err := ParseSymlinksFile([]byte(`{"version":99,"symlinks":{"link1":{"target":"t1"}}}`))
	t.Assert(err, IsNil)
	t.Assert(data.Version, Equals, 99)
	target, ok := data.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "t1")
}

func (s *SymlinksTest) TestDeepCopy(t *C) {
	original := NewSymlinksFileData()
	original.AddSymlink("link1", "../target1")
	original.AddSymlink("link2", "../target2")

	copied := original.DeepCopy()

	// Verify copy has the same data
	t.Assert(copied.Version, Equals, original.Version)
	t.Assert(len(copied.Symlinks), Equals, 2)
	target, ok := copied.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../target1")

	// Mutate the copy and verify original is unchanged
	copied.AddSymlink("link3", "../target3")
	copied.RemoveSymlink("link1")

	t.Assert(original.HasSymlink("link1"), Equals, true)
	t.Assert(original.HasSymlink("link3"), Equals, false)
	t.Assert(len(original.Symlinks), Equals, 2)

	t.Assert(copied.HasSymlink("link1"), Equals, false)
	t.Assert(copied.HasSymlink("link3"), Equals, true)
	t.Assert(len(copied.Symlinks), Equals, 2)
}

func (s *SymlinksTest) TestUpdateFailureDoesNotCorruptCache(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a symlinks file
	data := NewSymlinksFileData()
	data.AddSymlink("existing-link", "../existing-target")
	etag, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "")
	t.Assert(err, IsNil)

	// Simulate a cache: deep copy before mutating
	cached := data.DeepCopy()
	workingCopy := cached.DeepCopy()
	workingCopy.AddSymlink("new-link", "../new-target")

	// Simulate a failed save (wrong ETag)
	_, err = SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", workingCopy, "\"wrong-etag\"")
	t.Assert(err, NotNil)

	// Original cache must be untouched
	t.Assert(cached.HasSymlink("existing-link"), Equals, true)
	t.Assert(cached.HasSymlink("new-link"), Equals, false)
	t.Assert(len(cached.Symlinks), Equals, 1)

	_ = etag
}

func (s *SymlinksTest) TestGetSymlinksFilePath(t *C) {
	t.Assert(getSymlinksFilePath("", ".geesefs_symlinks"), Equals, ".geesefs_symlinks")
	t.Assert(getSymlinksFilePath("dir", ".geesefs_symlinks"), Equals, "dir/.geesefs_symlinks")
	t.Assert(getSymlinksFilePath("dir/", ".geesefs_symlinks"), Equals, "dir/.geesefs_symlinks")
	t.Assert(getSymlinksFilePath("path/to/dir", ".geesefs_symlinks"), Equals, "path/to/dir/.geesefs_symlinks")
}

func (s *SymlinksTest) TestSymlinksToFilesAndDirectories(t *C) {
	data := NewSymlinksFileData()

	// Add symlinks to files
	data.AddSymlink("link-to-file", "../file.txt")
	data.AddSymlink("link-to-nested-file", "../../other/path/file.dat")

	// Add symlinks to directories (relative)
	data.AddSymlink("link-to-dir", "../other-folder")
	data.AddSymlink("link-to-nested-dir", "../../parent/sibling-folder")

	// Add symlinks to directories (absolute)
	data.AddSymlink("link-to-abs-dir", "/absolute/path/to/folder")

	// Verify all symlinks are stored correctly
	t.Assert(len(data.Symlinks), Equals, 5)

	// File symlinks
	target, ok := data.GetSymlink("link-to-file")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../file.txt")

	target, ok = data.GetSymlink("link-to-nested-file")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../../other/path/file.dat")

	// Directory symlinks (relative)
	target, ok = data.GetSymlink("link-to-dir")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../other-folder")

	target, ok = data.GetSymlink("link-to-nested-dir")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../../parent/sibling-folder")

	// Directory symlinks (absolute)
	target, ok = data.GetSymlink("link-to-abs-dir")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "/absolute/path/to/folder")

	// Serialize and parse to ensure persistence works
	bytes, err := data.Serialize()
	t.Assert(err, IsNil)

	parsed, err := ParseSymlinksFile(bytes)
	t.Assert(err, IsNil)
	t.Assert(len(parsed.Symlinks), Equals, 5)

	// Verify directory symlinks survive serialization
	target, ok = parsed.GetSymlink("link-to-dir")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../other-folder")

	target, ok = parsed.GetSymlink("link-to-abs-dir")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "/absolute/path/to/folder")
}

func (s *SymlinksTest) TestSymlinkTargetWithSpecialCharacters(t *C) {
	data := NewSymlinksFileData()

	// Test targets with spaces
	data.AddSymlink("link-with-space", "../folder with spaces/file.txt")

	// Test targets with unicode
	data.AddSymlink("link-unicode", "../données/fichier.txt")

	// Test targets with dots
	data.AddSymlink("link-dots", "../.hidden-folder/.hidden-file")

	// Serialize and parse
	bytes, err := data.Serialize()
	t.Assert(err, IsNil)

	parsed, err := ParseSymlinksFile(bytes)
	t.Assert(err, IsNil)

	target, ok := parsed.GetSymlink("link-with-space")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../folder with spaces/file.txt")

	target, ok = parsed.GetSymlink("link-unicode")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../données/fichier.txt")

	target, ok = parsed.GetSymlink("link-dots")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../.hidden-folder/.hidden-file")
}

// ============================================================================
// Tests for Load/Save/Delete symlinks file operations with cloud storage
// These tests use mockConditionalBackend from backend_test.go
// ============================================================================

func (s *SymlinksTest) TestSaveSymlinksFileCreateNew(t *C) {
	mock := newMockConditionalBackend()

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Save new file (no expectedETag)
	etag, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "")
	t.Assert(err, IsNil)
	t.Assert(etag != "", Equals, true)

	// Verify it was saved
	_, exists := mock.objects["testdir/.geesefs_symlinks"]
	t.Assert(exists, Equals, true)
}

func (s *SymlinksTest) TestSaveSymlinksFileCreateNewPreventsOverwrite(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create an existing file
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{}}`),
		etag: "\"existing\"",
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Try to create new file (no expectedETag) - should fail because file exists
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "")
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*PreconditionFailed.*")
}

func (s *SymlinksTest) TestSaveSymlinksFileUpdateWithCorrectETag(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create an existing file
	existingETag := "\"existing-etag\""
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"old":"../old-target"}}`),
		etag: existingETag,
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Update with correct ETag - should succeed
	newETag, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, existingETag)
	t.Assert(err, IsNil)
	t.Assert(newETag != "", Equals, true)
	t.Assert(newETag != existingETag, Equals, true)
}

func (s *SymlinksTest) TestSaveSymlinksFileUpdateWithWrongETag(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create an existing file
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"old":"../old-target"}}`),
		etag: "\"actual-etag\"",
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Update with wrong ETag - should fail (optimistic locking)
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "\"wrong-etag\"")
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*PreconditionFailed.*")
}

func (s *SymlinksTest) TestLoadSymlinksFile(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file
	existingETag := "\"test-etag\""
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"link1":{"target":"../target1"}}}`),
		etag: existingETag,
	}

	data, etag, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)
	t.Assert(etag, Equals, existingETag)
	t.Assert(data.HasSymlink("link1"), Equals, true)

	target, ok := data.GetSymlink("link1")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "../target1")
}

func (s *SymlinksTest) TestLoadSymlinksFileNotFound(t *C) {
	mock := newMockConditionalBackend()

	data, etag, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)
	t.Assert(etag, Equals, "")
	t.Assert(data.IsEmpty(), Equals, true)
}

func (s *SymlinksTest) TestDeleteSymlinksFile(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{}}`),
		etag: "\"test\"",
	}

	err := DeleteSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)

	_, exists := mock.objects["testdir/.geesefs_symlinks"]
	t.Assert(exists, Equals, false)
}

func (s *SymlinksTest) TestSaveEmptySymlinksFileDeletesExisting(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create an existing file
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"link1":{"target":"../target1"}}}`),
		etag: "\"existing\"",
	}

	// Save empty data with existing ETag should delete the file
	emptyData := NewSymlinksFileData()
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", emptyData, "\"existing\"")
	t.Assert(err, IsNil)

	_, exists := mock.objects["testdir/.geesefs_symlinks"]
	t.Assert(exists, Equals, false)
}

func (s *SymlinksTest) TestSaveEmptySymlinksFileRaceDetection(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create an existing file
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"link1":{"target":"../target1"}}}`),
		etag: "\"original\"",
	}

	// Simulate another mount modifying the file before our delete
	// by providing a stale ETag
	emptyData := NewSymlinksFileData()
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", emptyData, "\"stale\"")
	t.Assert(err, NotNil)
	t.Assert(isPreconditionFailed(err), Equals, true)

	// File should still exist with the other mount's data
	obj, exists := mock.objects["testdir/.geesefs_symlinks"]
	t.Assert(exists, Equals, true)
	t.Assert(string(obj.data), Matches, ".*link1.*")
}

func (s *SymlinksTest) TestConcurrentSymlinkCreation(t *C) {
	mock := newMockConditionalBackend()

	// Simulate two concurrent creations - first one wins
	data1 := NewSymlinksFileData()
	data1.AddSymlink("link1", "../target1")

	data2 := NewSymlinksFileData()
	data2.AddSymlink("link2", "../target2")

	// First creation succeeds
	etag1, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data1, "")
	t.Assert(err, IsNil)
	t.Assert(etag1 != "", Equals, true)

	// Second creation fails (file already exists)
	_, err = SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data2, "")
	t.Assert(err, NotNil)

	// Proper way: load, merge, save with ETag
	existingData, etag, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)
	t.Assert(etag, Equals, etag1)

	existingData.AddSymlink("link2", "../target2")
	newETag, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", existingData, etag)
	t.Assert(err, IsNil)
	t.Assert(newETag != etag, Equals, true)

	// Verify both symlinks exist
	finalData, _, err := LoadSymlinksFile(mock, "testdir", ".geesefs_symlinks")
	t.Assert(err, IsNil)
	t.Assert(finalData.HasSymlink("link1"), Equals, true)
	t.Assert(finalData.HasSymlink("link2"), Equals, true)
}

// ============================================================================
// Tests for verifying SaveSymlinksFile uses correct conditional write headers
// ============================================================================

func (s *SymlinksTest) TestSaveSymlinksFileUsesIfNoneMatchForNewFile(t *C) {
	mock := newMockConditionalBackend()

	var capturedIfNoneMatch *string
	mock.onPutBlob = func(param *PutBlobInput) {
		capturedIfNoneMatch = param.IfNoneMatch
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Save new file - should use If-None-Match: "*"
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, "")
	t.Assert(err, IsNil)
	t.Assert(capturedIfNoneMatch, NotNil)
	t.Assert(*capturedIfNoneMatch, Equals, "*")
}

func (s *SymlinksTest) TestSaveSymlinksFileUsesIfMatchForUpdate(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create existing file
	existingETag := "\"existing-etag\""
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{}}`),
		etag: existingETag,
	}

	var capturedIfMatch *string
	mock.onPutBlob = func(param *PutBlobInput) {
		capturedIfMatch = param.IfMatch
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	// Update with ETag - should use If-Match
	_, err := SaveSymlinksFile(mock, "testdir", ".geesefs_symlinks", data, existingETag)
	t.Assert(err, IsNil)
	t.Assert(capturedIfMatch, NotNil)
	t.Assert(*capturedIfMatch, Equals, existingETag)
}

// ============================================================================
// Tests for SaveSymlinksFileWithRetry (exponential backoff)
// ============================================================================

func (s *SymlinksTest) TestSaveWithRetrySucceedsOnFirstAttempt(t *C) {
	mock := newMockConditionalBackend()

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		t.Fatal("Merge function should not be called on first success")
		return nil, nil
	}

	newETag, err := SaveSymlinksFileWithRetry(mock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 3)
	t.Assert(err, IsNil)
	t.Assert(newETag, Not(Equals), "")
}

func (s *SymlinksTest) TestSaveWithRetryRetriesOnConflict(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file to cause initial conflict
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"existing":{"target":"../old","mtime":1}}}`),
		etag: "\"etag-v1\"",
	}

	data := NewSymlinksFileData()
	data.AddSymlink("newlink", "../newtarget")

	mergeCallCount := 0
	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		mergeCallCount++
		// Merge: keep existing symlinks and add our new one
		current.AddSymlink("newlink", "../newtarget")
		return current, nil
	}

	// Try to create (If-None-Match: "*") - will fail, then retry with merge
	newETag, err := SaveSymlinksFileWithRetry(mock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 3)
	t.Assert(err, IsNil)
	t.Assert(newETag, Not(Equals), "")
	t.Assert(mergeCallCount, Equals, 1)

	// Verify merged result contains both symlinks
	obj := mock.objects["testdir/.geesefs_symlinks"]
	parsed, _ := ParseSymlinksFile(obj.data)
	t.Assert(parsed.HasSymlink("existing"), Equals, true)
	t.Assert(parsed.HasSymlink("newlink"), Equals, true)
}

func (s *SymlinksTest) TestSaveWithRetryExceedsMaxRetries(t *C) {
	mock := newMockConditionalBackend()

	// Create a mock that always fails with precondition error
	failingMock := &alwaysConflictBackend{mock}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	mergeCallCount := 0
	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		mergeCallCount++
		return data, nil
	}

	_, err := SaveSymlinksFileWithRetry(failingMock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 2)
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*max retries.*exceeded.*")
	t.Assert(mergeCallCount, Equals, 2) // Should have tried merge twice
}

func (s *SymlinksTest) TestSaveWithRetryMergeFunctionError(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file to cause conflict
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{}}`),
		etag: "\"etag-v1\"",
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		return nil, fmt.Errorf("merge conflict: cannot resolve")
	}

	_, err := SaveSymlinksFileWithRetry(mock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 3)
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*merge function failed.*")
}

func (s *SymlinksTest) TestSaveWithRetryNoRetriesOnOtherErrors(t *C) {
	mock := newMockConditionalBackend()

	// Create a mock that returns a non-precondition error
	errorMock := &errorBackend{mock, fmt.Errorf("network error: connection refused")}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	mergeCallCount := 0
	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		mergeCallCount++
		return data, nil
	}

	_, err := SaveSymlinksFileWithRetry(errorMock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 3)
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*network error.*")
	t.Assert(mergeCallCount, Equals, 0) // Should not have tried merge
}

func (s *SymlinksTest) TestSaveWithRetryZeroMaxRetries(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file to cause conflict
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{}}`),
		etag: "\"etag-v1\"",
	}

	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")

	mergeCallCount := 0
	mergeFn := func(current *SymlinksFileData) (*SymlinksFileData, error) {
		mergeCallCount++
		return data, nil
	}

	// With maxRetries=0, should fail immediately on conflict
	_, err := SaveSymlinksFileWithRetry(mock, "testdir", ".geesefs_symlinks", data, "", mergeFn, 0)
	t.Assert(err, NotNil)
	t.Assert(err.Error(), Matches, ".*max retries.*exceeded.*")
	t.Assert(mergeCallCount, Equals, 0)
}

// alwaysConflictBackend wraps a backend and always returns precondition failed on PutBlob
type alwaysConflictBackend struct {
	*mockConditionalBackend
}

func (m *alwaysConflictBackend) PutBlob(param *PutBlobInput) (*PutBlobOutput, error) {
	// Always return precondition failed
	return nil, fmt.Errorf("PreconditionFailed: simulated conflict")
}

// errorBackend wraps a backend and returns a specific error on PutBlob
type errorBackend struct {
	*mockConditionalBackend
	err error
}

func (m *errorBackend) PutBlob(param *PutBlobInput) (*PutBlobOutput, error) {
	return nil, m.err
}

// ============================================================================
// Tests for LoadSymlinksFileConditional (conditional GET / 304 Not Modified)
// ============================================================================

func (s *SymlinksTest) TestLoadSymlinksFileConditionalNotModified(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file
	existingETag := "\"test-etag-v1\""
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"link1":{"target":"../target1","mtime":1}}}`),
		etag: existingETag,
	}

	// First load - should return data
	data1, etag1, err := LoadSymlinksFileConditional(mock, "testdir", ".geesefs_symlinks", "")
	t.Assert(err, IsNil)
	t.Assert(data1, NotNil)
	t.Assert(etag1, Equals, existingETag)
	t.Assert(data1.HasSymlink("link1"), Equals, true)

	// Second load with same ETag - should return nil (304 Not Modified)
	data2, etag2, err := LoadSymlinksFileConditional(mock, "testdir", ".geesefs_symlinks", existingETag)
	t.Assert(err, IsNil)
	t.Assert(data2, IsNil) // nil indicates no change
	t.Assert(etag2, Equals, existingETag)
}

func (s *SymlinksTest) TestLoadSymlinksFileConditionalModified(t *C) {
	mock := newMockConditionalBackend()

	// Pre-create a file
	existingETag := "\"test-etag-v1\""
	mock.objects["testdir/.geesefs_symlinks"] = &mockStoredObject{
		data: []byte(`{"version":1,"symlinks":{"link1":{"target":"../target1","mtime":1}}}`),
		etag: existingETag,
	}

	// Load with outdated ETag - should return new data
	data, etag, err := LoadSymlinksFileConditional(mock, "testdir", ".geesefs_symlinks", "\"old-etag\"")
	t.Assert(err, IsNil)
	t.Assert(data, NotNil)
	t.Assert(etag, Equals, existingETag)
	t.Assert(data.HasSymlink("link1"), Equals, true)
}

func (s *SymlinksTest) TestLoadSymlinksFileConditionalFileDeleted(t *C) {
	mock := newMockConditionalBackend()

	// File doesn't exist - should return empty data regardless of cachedETag
	data, etag, err := LoadSymlinksFileConditional(mock, "testdir", ".geesefs_symlinks", "\"some-old-etag\"")
	t.Assert(err, IsNil)
	t.Assert(data, NotNil) // Returns empty data, not nil
	t.Assert(data.IsEmpty(), Equals, true)
	t.Assert(etag, Equals, "")
}

// ============================================================================
// Tests for symlinks cache lookup scenarios (simulates LookUpCached behavior)
// ============================================================================

func (s *SymlinksTest) TestSymlinksCacheLookupWhenInodeNotInChildren(t *C) {
	// This test simulates the scenario where:
	// - Mount 1 creates a symlink (updates .geesefs_symlinks)
	// - Mount 2's directory cache is valid but doesn't have the inode
	// - Mount 2 should find the symlink via the symlinks cache

	data := NewSymlinksFileData()
	data.AddSymlink("new-symlink", "../target-file.txt")

	// Verify the symlink can be found in the cache
	target, found := data.GetSymlink("new-symlink")
	t.Assert(found, Equals, true)
	t.Assert(target, Equals, "../target-file.txt")

	// Verify non-existent symlinks return false
	_, found = data.GetSymlink("not-a-symlink")
	t.Assert(found, Equals, false)
}

func (s *SymlinksTest) TestSymlinksCacheUpdatesMergeCorrectly(t *C) {
	// Simulates concurrent updates from different mounts

	// Mount 1 has these symlinks
	mount1Data := NewSymlinksFileData()
	mount1Data.AddSymlink("link-from-mount1", "../target1")

	// Mount 2 has these symlinks
	mount2Data := NewSymlinksFileData()
	mount2Data.AddSymlink("link-from-mount2", "../target2")

	// Simulate merge: Mount 2 loads Mount 1's data and adds its symlink
	mergedData := NewSymlinksFileData()
	mergedData.AddSymlink("link-from-mount1", "../target1") // From cloud
	mergedData.AddSymlink("link-from-mount2", "../target2") // Local addition

	// Verify both symlinks are present
	t.Assert(mergedData.HasSymlink("link-from-mount1"), Equals, true)
	t.Assert(mergedData.HasSymlink("link-from-mount2"), Equals, true)

	target1, _ := mergedData.GetSymlink("link-from-mount1")
	target2, _ := mergedData.GetSymlink("link-from-mount2")
	t.Assert(target1, Equals, "../target1")
	t.Assert(target2, Equals, "../target2")
}

func (s *SymlinksTest) TestSymlinksCacheDeleteMergesCorrectly(t *C) {
	// Simulates deleting a symlink when another mount added symlinks

	// Cloud has both symlinks
	cloudData := NewSymlinksFileData()
	cloudData.AddSymlink("link1", "../target1")
	cloudData.AddSymlink("link2", "../target2")

	// Mount wants to delete link1
	cloudData.RemoveSymlink("link1")

	// Verify only link2 remains
	t.Assert(cloudData.HasSymlink("link1"), Equals, false)
	t.Assert(cloudData.HasSymlink("link2"), Equals, true)
}

// ============================================================================
// Tests for error detection functions (AWS SDK typed errors + fallbacks)
// ============================================================================

func (s *SymlinksTest) TestIsNotExistWithAwsRequestFailure(t *C) {
	// Real S3 backend returns awserr.RequestFailure with status 404
	err := awserr.NewRequestFailure(awserr.New("NoSuchKey", "The specified key does not exist.", nil), 404, "req-123")
	t.Assert(isNotExist(err), Equals, true)
}

func (s *SymlinksTest) TestIsNotExistWithAwsErrorCode(t *C) {
	err := awserr.New("NoSuchKey", "key not found", nil)
	t.Assert(isNotExist(err), Equals, true)

	err = awserr.New("NotFound", "not found", nil)
	t.Assert(isNotExist(err), Equals, true)
}

func (s *SymlinksTest) TestIsNotExistWithSyscallENOENT(t *C) {
	t.Assert(isNotExist(syscall.ENOENT), Equals, true)
}

func (s *SymlinksTest) TestIsNotExistWithNilAndUnrelated(t *C) {
	t.Assert(isNotExist(nil), Equals, false)
	t.Assert(isNotExist(fmt.Errorf("error at line 404")), Equals, false)
	t.Assert(isNotExist(fmt.Errorf("network timeout")), Equals, false)
}

func (s *SymlinksTest) TestIsPreconditionFailedWithAwsRequestFailure(t *C) {
	err := awserr.NewRequestFailure(awserr.New("PreconditionFailed", "At least one of the pre-conditions you specified did not hold", nil), 412, "req-456")
	t.Assert(isPreconditionFailed(err), Equals, true)
}

func (s *SymlinksTest) TestIsPreconditionFailedWithAwsErrorCode(t *C) {
	err := awserr.New("PreconditionFailed", "precondition failed", nil)
	t.Assert(isPreconditionFailed(err), Equals, true)
}

func (s *SymlinksTest) TestIsPreconditionFailedWithMockError(t *C) {
	// Mock backend returns plain fmt.Errorf starting with "PreconditionFailed"
	t.Assert(isPreconditionFailed(fmt.Errorf("PreconditionFailed: object already exists")), Equals, true)
	t.Assert(isPreconditionFailed(fmt.Errorf("PreconditionFailed: ETag mismatch")), Equals, true)
}

func (s *SymlinksTest) TestIsPreconditionFailedWithNilAndUnrelated(t *C) {
	t.Assert(isPreconditionFailed(nil), Equals, false)
	t.Assert(isPreconditionFailed(fmt.Errorf("error code 412 in config")), Equals, false)
	t.Assert(isPreconditionFailed(fmt.Errorf("network timeout")), Equals, false)
}

func (s *SymlinksTest) TestIsNotModifiedWithAwsRequestFailure(t *C) {
	err := awserr.NewRequestFailure(awserr.New("NotModified", "Not Modified", nil), 304, "req-789")
	t.Assert(isNotModified(err), Equals, true)
}

func (s *SymlinksTest) TestIsNotModifiedWithAwsErrorCode(t *C) {
	err := awserr.New("NotModified", "not modified", nil)
	t.Assert(isNotModified(err), Equals, true)
}

func (s *SymlinksTest) TestIsNotModifiedWithMockError(t *C) {
	t.Assert(isNotModified(fmt.Errorf("304 Not Modified")), Equals, true)
}

func (s *SymlinksTest) TestIsNotModifiedWithNilAndUnrelated(t *C) {
	t.Assert(isNotModified(nil), Equals, false)
	t.Assert(isNotModified(fmt.Errorf("error 304 in processing")), Equals, false)
	t.Assert(isNotModified(fmt.Errorf("network timeout")), Equals, false)
}

// ============================================================================
// Helper: creates a minimal Goofys + directory inode for batching tests
// ============================================================================

func newTestDirInode(mock StorageBackend, batchDelay time.Duration) (*Goofys, *Inode) {
	flags := cfg.DefaultFlags()
	flags.EnableSymlinksFile = true
	flags.SymlinksBatchDelay = batchDelay
	flags.StatCacheTTL = 30 * time.Second

	fs := &Goofys{
		flags:            flags,
		inodes:           make(map[fuseops.InodeID]*Inode),
		nextInodeID:      fuseops.RootInodeID + 1,
		inflightChanges:  make(map[string]int),
		inflightListings: make(map[int]map[string]bool),
	}
	fs.flusherCond = sync.NewCond(&fs.flusherMu)

	root := &Inode{
		Name: "",
		fs:   fs,
		Id:   fuseops.RootInodeID,
		dir: &DirInodeData{
			cloud:          mock,
			mountPrefix:    "",
			lastOpenDirIdx: -1,
		},
	}
	fs.inodes[fuseops.RootInodeID] = root
	return fs, root
}

// ============================================================================
// Tests for symlinks batching
// ============================================================================

func (s *SymlinksTest) TestBatchingQueuesChangesAndUpdatesCache(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour) // long delay so timer doesn't fire

	parent.mu.Lock()

	// Create first symlink
	err := parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)

	// Cache should be updated eagerly
	t.Assert(parent.dir.symlinksCache.HasSymlink("link1"), Equals, true)

	// Pending queue should have one entry
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 1)
	t.Assert(parent.dir.pendingSymlinkChanges[0].Name, Equals, "link1")

	// Create second symlink
	err = parent.updateSymlinksFile("link2", "../target2", false)
	t.Assert(err, IsNil)

	// Cache should have both
	t.Assert(parent.dir.symlinksCache.HasSymlink("link1"), Equals, true)
	t.Assert(parent.dir.symlinksCache.HasSymlink("link2"), Equals, true)

	// Pending queue should have two entries
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 2)

	// No S3 PUT should have happened yet
	_, exists := mock.objects[".geesefs_symlinks"]
	t.Assert(exists, Equals, false)

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestBatchFlushCoalescesChanges(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	parent.mu.Lock()

	// Create 10 symlinks
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("link%d", i)
		target := fmt.Sprintf("../target%d", i)
		err := parent.updateSymlinksFile(name, target, false)
		t.Assert(err, IsNil)
	}

	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 10)

	// Count PutBlob calls
	putCount := 0
	mock.onPutBlob = func(param *PutBlobInput) {
		putCount++
	}

	// Flush should produce a single S3 PUT
	err := parent.flushSymlinksChanges()
	t.Assert(err, IsNil)
	t.Assert(putCount, Equals, 1)

	// Pending queue should be empty
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 0)

	// S3 should have all 10 symlinks
	obj := mock.objects[".geesefs_symlinks"]
	t.Assert(obj, NotNil)
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	t.Assert(len(parsed.Symlinks), Equals, 10)

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestBatchMergeReplaysAllChangesOnConflict(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	// Pre-create a symlinks file in S3 (simulating another mount)
	otherData := NewSymlinksFileData()
	otherData.AddSymlink("other-link", "../other-target")
	etag, err := SaveSymlinksFile(mock, "", ".geesefs_symlinks", otherData, "")
	t.Assert(err, IsNil)

	parent.mu.Lock()

	// Load cache (gets the existing file)
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)
	t.Assert(parent.dir.symlinksCache.HasSymlink("other-link"), Equals, true)

	// Queue two changes
	err = parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)
	err = parent.updateSymlinksFile("link2", "../target2", false)
	t.Assert(err, IsNil)

	// Simulate a concurrent modification: another mount changed the file
	otherData2 := NewSymlinksFileData()
	otherData2.AddSymlink("other-link", "../other-target")
	otherData2.AddSymlink("concurrent-link", "../concurrent-target")
	// Overwrite directly (bypassing conditional checks for test setup)
	mock.mu.Lock()
	serialized, _ := otherData2.Serialize()
	mock.objects[".geesefs_symlinks"] = &mockStoredObject{
		data: serialized,
		etag: "\"concurrent-etag\"",
	}
	mock.mu.Unlock()

	// Flush with stale ETag - should trigger merge
	err = parent.flushSymlinksChanges()
	t.Assert(err, IsNil)

	// Result should have all 4 symlinks (other-link, concurrent-link, link1, link2)
	obj := mock.objects[".geesefs_symlinks"]
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	t.Assert(parsed.HasSymlink("other-link"), Equals, true)
	t.Assert(parsed.HasSymlink("concurrent-link"), Equals, true)
	t.Assert(parsed.HasSymlink("link1"), Equals, true)
	t.Assert(parsed.HasSymlink("link2"), Equals, true)

	_ = etag
	parent.mu.Unlock()
}

func (s *SymlinksTest) TestBatchOrderPreservation(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	parent.mu.Lock()

	// ln -s a foo && rm foo && ln -s b foo
	err := parent.updateSymlinksFile("foo", "a", false)
	t.Assert(err, IsNil)
	err = parent.updateSymlinksFile("foo", "", true)
	t.Assert(err, IsNil)
	err = parent.updateSymlinksFile("foo", "b", false)
	t.Assert(err, IsNil)

	// In-memory cache should show foo -> b
	target, ok := parent.dir.symlinksCache.GetSymlink("foo")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "b")

	// Flush
	err = parent.flushSymlinksChanges()
	t.Assert(err, IsNil)

	// S3 should have foo -> b
	obj := mock.objects[".geesefs_symlinks"]
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	target, ok = parsed.GetSymlink("foo")
	t.Assert(ok, Equals, true)
	t.Assert(target, Equals, "b")

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestNoBatchingFlushesImmediately(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 0) // batching disabled

	parent.mu.Lock()

	// Create a symlink - should flush immediately
	err := parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)

	// Pending queue should be empty (already flushed)
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 0)

	// S3 should have the symlink
	obj := mock.objects[".geesefs_symlinks"]
	t.Assert(obj, NotNil)
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	t.Assert(parsed.HasSymlink("link1"), Equals, true)

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestCacheReloadPreservesPendingChanges(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	parent.mu.Lock()

	// Create initial symlinks and flush them
	err := parent.updateSymlinksFile("existing", "../existing-target", false)
	t.Assert(err, IsNil)
	err = parent.flushSymlinksChanges()
	t.Assert(err, IsNil)

	// Queue a new change (not yet flushed)
	err = parent.updateSymlinksFile("pending", "../pending-target", false)
	t.Assert(err, IsNil)
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 1)

	// Force cache to expire by setting time in the past
	parent.dir.symlinksCacheTime = time.Time{}

	// Reload the cache from S3
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)

	// The pending change should still be visible in the cache
	t.Assert(parent.dir.symlinksCache.HasSymlink("pending"), Equals, true)
	t.Assert(parent.dir.symlinksCache.HasSymlink("existing"), Equals, true)

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestFlushErrorRequeuesChanges(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	// Pre-create a symlinks file with a different ETag to cause conflict
	otherData := NewSymlinksFileData()
	etag, err := SaveSymlinksFile(mock, "", ".geesefs_symlinks", otherData, "")
	t.Assert(err, IsNil)

	parent.mu.Lock()

	// Load cache
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)

	// Queue a change
	err = parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)

	// Replace the backend with one that always fails
	failMock := &alwaysConflictBackend{mock}
	parent.dir.cloud = failMock

	// Flush should fail
	err = parent.flushSymlinksChanges()
	t.Assert(err, NotNil)

	// Changes should be re-queued
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 1)
	t.Assert(parent.dir.pendingSymlinkChanges[0].Name, Equals, "link1")

	// In-memory cache should still have the change
	t.Assert(parent.dir.symlinksCache.HasSymlink("link1"), Equals, true)

	_ = etag
	parent.mu.Unlock()
}

func (s *SymlinksTest) TestFlushPendingSymlinksPublicAPI(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	parent.mu.Lock()

	// Queue changes
	err := parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)
	err = parent.updateSymlinksFile("link2", "../target2", false)
	t.Assert(err, IsNil)

	parent.mu.Unlock()

	// Use public API (acquires lock internally)
	err = parent.FlushPendingSymlinks()
	t.Assert(err, IsNil)

	// Verify everything was flushed
	parent.mu.Lock()
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 0)
	parent.mu.Unlock()

	obj := mock.objects[".geesefs_symlinks"]
	t.Assert(obj, NotNil)
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	t.Assert(parsed.HasSymlink("link1"), Equals, true)
	t.Assert(parsed.HasSymlink("link2"), Equals, true)
}

func (s *SymlinksTest) TestFlushPendingSymlinksNoopWhenEmpty(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	// No pending changes - should be a no-op
	err := parent.FlushPendingSymlinks()
	t.Assert(err, IsNil)

	// No S3 calls should have been made
	t.Assert(len(mock.objects), Equals, 0)
}

func (s *SymlinksTest) TestBatchTimerFiresAndFlushes(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 50*time.Millisecond)

	parent.mu.Lock()

	// Queue a change (timer should start)
	err := parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)

	// Timer should be set
	t.Assert(parent.dir.symlinkFlushTimer, NotNil)

	parent.mu.Unlock()

	// Wait for timer to fire
	time.Sleep(200 * time.Millisecond)

	// Verify the flush happened
	parent.mu.Lock()
	t.Assert(len(parent.dir.pendingSymlinkChanges), Equals, 0)
	parent.mu.Unlock()

	obj := mock.objects[".geesefs_symlinks"]
	t.Assert(obj, NotNil)
	parsed, err := ParseSymlinksFile(obj.data)
	t.Assert(err, IsNil)
	t.Assert(parsed.HasSymlink("link1"), Equals, true)
}

func (s *SymlinksTest) TestBatchRemoveOnlySymlink(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 0) // immediate flush

	parent.mu.Lock()

	// Create a symlink
	err := parent.updateSymlinksFile("link1", "../target1", false)
	t.Assert(err, IsNil)

	// Remove it
	err = parent.updateSymlinksFile("link1", "", true)
	t.Assert(err, IsNil)

	// Empty data should have deleted the file
	_, exists := mock.objects[".geesefs_symlinks"]
	t.Assert(exists, Equals, false)

	parent.mu.Unlock()
}

func (s *SymlinksTest) TestCacheNotModifiedRearmsTTL(t *C) {
	mock := newMockConditionalBackend()
	_, parent := newTestDirInode(mock, 1*time.Hour)

	// Give the directory a symlinks file so the reload can come back 304.
	data := NewSymlinksFileData()
	data.AddSymlink("link1", "../target1")
	_, err := SaveSymlinksFile(mock, "", ".geesefs_symlinks", data, "")
	t.Assert(err, IsNil)

	gets := 0
	mock.onGetBlob = func(param *GetBlobInput) { gets++ }

	parent.mu.Lock()
	defer parent.mu.Unlock()

	// First load populates the cache and records the ETag.
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)
	t.Assert(gets, Equals, 1)
	t.Assert(parent.dir.symlinksCache.HasSymlink("link1"), Equals, true)

	// Expire the cache; the reload sends If-None-Match and gets a 304 back.
	parent.dir.symlinksCacheTime = time.Time{}
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)
	t.Assert(gets, Equals, 2)

	// The 304 confirmed the cache is current, so the next miss within the TTL
	// must be served locally instead of re-issuing the conditional GET.
	err = parent.loadSymlinksCache()
	t.Assert(err, IsNil)
	t.Assert(gets, Equals, 2)
	t.Assert(parent.dir.symlinksCache.HasSymlink("link1"), Equals, true)
}
