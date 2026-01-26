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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SymlinkEntry represents a single symlink in the .symlinks file
type SymlinkEntry struct {
	Target string `json:"target"`
	Mtime  int64  `json:"mtime"`
}

// SymlinksFileData represents the content of a .symlinks file
type SymlinksFileData struct {
	Version  int                     `json:"version"`
	Symlinks map[string]SymlinkEntry `json:"symlinks"`
}

// SymlinksFileCache caches the .symlinks file data for a directory
type SymlinksFileCache struct {
	mu       sync.RWMutex
	data     *SymlinksFileData
	etag     string
	loadTime time.Time
}

// NewSymlinksFileData creates a new empty symlinks file data structure
func NewSymlinksFileData() *SymlinksFileData {
	return &SymlinksFileData{
		Version:  1,
		Symlinks: make(map[string]SymlinkEntry),
	}
}

// ParseSymlinksFile parses a .symlinks file content
func ParseSymlinksFile(data []byte) (*SymlinksFileData, error) {
	if len(data) == 0 {
		return NewSymlinksFileData(), nil
	}

	var result SymlinksFileData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	if result.Symlinks == nil {
		result.Symlinks = make(map[string]SymlinkEntry)
	}

	return &result, nil
}

// Serialize converts the symlinks data to JSON bytes
func (s *SymlinksFileData) Serialize() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// AddSymlink adds or updates a symlink entry
func (s *SymlinksFileData) AddSymlink(name, target string) {
	s.Symlinks[name] = SymlinkEntry{
		Target: target,
		Mtime:  time.Now().Unix(),
	}
}

// RemoveSymlink removes a symlink entry
func (s *SymlinksFileData) RemoveSymlink(name string) {
	delete(s.Symlinks, name)
}

// GetSymlink returns the target for a symlink, or empty string if not found
func (s *SymlinksFileData) GetSymlink(name string) (string, bool) {
	entry, ok := s.Symlinks[name]
	if !ok {
		return "", false
	}
	return entry.Target, true
}

// HasSymlink checks if a symlink exists
func (s *SymlinksFileData) HasSymlink(name string) bool {
	_, ok := s.Symlinks[name]
	return ok
}

// IsEmpty returns true if there are no symlinks
func (s *SymlinksFileData) IsEmpty() bool {
	return len(s.Symlinks) == 0
}

// getSymlinksFilePath returns the full key path for the .symlinks file in a directory
func getSymlinksFilePath(dirKey string, symlinksFileName string) string {
	if dirKey == "" {
		return symlinksFileName
	}
	if !strings.HasSuffix(dirKey, "/") {
		dirKey += "/"
	}
	return dirKey + symlinksFileName
}

// LoadSymlinksFile loads the .symlinks file from the cloud storage
// Returns the parsed data, the ETag (for conditional updates), and any error
func LoadSymlinksFile(cloud StorageBackend, dirKey string, symlinksFileName string) (*SymlinksFileData, string, error) {
	key := getSymlinksFilePath(dirKey, symlinksFileName)

	resp, err := cloud.GetBlob(&GetBlobInput{
		Key:   key,
		Start: 0,
		Count: 0, // Read entire file
	})

	if err != nil {
		// If file doesn't exist, return empty data
		if isNotExist(err) {
			return NewSymlinksFileData(), "", nil
		}
		return nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	parsed, err := ParseSymlinksFile(data)
	if err != nil {
		return nil, "", err
	}

	etag := ""
	if resp.ETag != nil {
		etag = *resp.ETag
	}

	return parsed, etag, nil
}

// SaveSymlinksFile saves the .symlinks file to cloud storage with conditional write
// If expectedETag is non-empty, uses conditional PUT to avoid race conditions
// Returns the new ETag and any error
func SaveSymlinksFile(cloud StorageBackend, dirKey string, symlinksFileName string, data *SymlinksFileData, expectedETag string) (string, error) {
	key := getSymlinksFilePath(dirKey, symlinksFileName)

	// If there are no symlinks and this is a new file, don't create it
	if data.IsEmpty() && expectedETag == "" {
		return "", nil
	}

	// If there are no symlinks, delete the file
	if data.IsEmpty() {
		_, err := cloud.DeleteBlob(&DeleteBlobInput{Key: key})
		if err != nil && !isNotExist(err) {
			return "", err
		}
		return "", nil
	}

	content, err := data.Serialize()
	if err != nil {
		return "", err
	}

	// Use conditional writes for optimistic locking (S3 feature since 2024):
	// - If expectedETag is empty, use If-None-Match: "*" to only create if file doesn't exist
	// - If expectedETag is provided, use If-Match to only update if ETag matches (optimistic locking)
	putInput := &PutBlobInput{
		Key:  key,
		Body: bytes.NewReader(content),
		Size: PUInt64(uint64(len(content))),
	}

	if expectedETag == "" {
		// Creating a new file - use If-None-Match to prevent overwriting
		ifNoneMatch := "*"
		putInput.IfNoneMatch = &ifNoneMatch
	} else {
		// Updating existing file - use If-Match for optimistic locking
		putInput.IfMatch = &expectedETag
	}

	resp, err := cloud.PutBlob(putInput)

	if err != nil {
		return "", err
	}

	newETag := ""
	if resp.ETag != nil {
		newETag = *resp.ETag
	}

	return newETag, nil
}

// SymlinksMergeFunc is called when a conflict is detected during save.
// It receives the current data from cloud storage and must return the merged data to save.
// The function should merge the caller's pending changes into currentData.
type SymlinksMergeFunc func(currentData *SymlinksFileData) (*SymlinksFileData, error)

// isPreconditionFailed checks if an error is a precondition failed error (412)
func isPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "PreconditionFailed") ||
		strings.Contains(errStr, "412") ||
		strings.Contains(errStr, "Precondition Failed") ||
		strings.Contains(errStr, "conditional request failed")
}

// SaveSymlinksFileWithRetry saves the .symlinks file with automatic retry on conflict.
// Uses exponential backoff and calls the merge function to resolve conflicts.
// Parameters:
//   - cloud: the storage backend
//   - dirKey: directory key (prefix)
//   - symlinksFileName: name of the symlinks file (e.g., ".symlinks")
//   - data: initial data to save
//   - expectedETag: current known ETag (empty for new files)
//   - mergeFn: function to merge changes on conflict (receives current cloud data)
//   - maxRetries: maximum number of retry attempts (0 for no retries)
//
// Returns the new ETag and any error.
func SaveSymlinksFileWithRetry(
	cloud StorageBackend,
	dirKey string,
	symlinksFileName string,
	data *SymlinksFileData,
	expectedETag string,
	mergeFn SymlinksMergeFunc,
	maxRetries int,
) (string, error) {
	const (
		initialBackoff = 50 * time.Millisecond
		maxBackoff     = 2 * time.Second
		backoffFactor  = 2.0
	)

	currentData := data
	currentETag := expectedETag
	backoff := initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Try to save
		newETag, err := SaveSymlinksFile(cloud, dirKey, symlinksFileName, currentData, currentETag)
		if err == nil {
			return newETag, nil
		}

		// If not a precondition failure, return the error immediately
		if !isPreconditionFailed(err) {
			return "", err
		}

		// Precondition failed - conflict detected
		if attempt >= maxRetries {
			return "", fmt.Errorf("symlinks file conflict: max retries (%d) exceeded: %w", maxRetries, err)
		}

		// Wait with exponential backoff before retrying
		time.Sleep(backoff)
		backoff = time.Duration(float64(backoff) * backoffFactor)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		// Re-read the current state from cloud storage
		cloudData, cloudETag, loadErr := LoadSymlinksFile(cloud, dirKey, symlinksFileName)
		if loadErr != nil {
			// If file was deleted, treat as empty
			if isNotExist(loadErr) {
				cloudData = NewSymlinksFileData()
				cloudETag = ""
			} else {
				return "", fmt.Errorf("failed to reload symlinks file during retry: %w", loadErr)
			}
		}

		// Call merge function to combine our changes with the cloud state
		mergedData, mergeErr := mergeFn(cloudData)
		if mergeErr != nil {
			return "", fmt.Errorf("merge function failed: %w", mergeErr)
		}

		// Update for next attempt
		currentData = mergedData
		currentETag = cloudETag
	}

	// Should not reach here
	return "", fmt.Errorf("symlinks file save failed unexpectedly")
}

// DeleteSymlinksFile removes the .symlinks file from cloud storage
func DeleteSymlinksFile(cloud StorageBackend, dirKey string, symlinksFileName string) error {
	key := getSymlinksFilePath(dirKey, symlinksFileName)
	_, err := cloud.DeleteBlob(&DeleteBlobInput{Key: key})
	if err != nil && !isNotExist(err) {
		return err
	}
	return nil
}

// Helper to check if an error indicates the object doesn't exist
func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	// Check for syscall error
	if err == syscall.ENOENT {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "NoSuchKey") ||
		strings.Contains(errStr, "NotFound") ||
		strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "does not exist") ||
		strings.Contains(errStr, "no such file or directory")
}
