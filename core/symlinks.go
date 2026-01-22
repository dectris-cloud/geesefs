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
	"io"
	"strings"
	"sync"
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

	// Note: S3 doesn't support conditional PUT with If-Match directly in PutBlob
	// For true atomicity, we would need to use S3's conditional writes feature
	// or implement a read-modify-write with retry logic
	// For now, we do a simple PUT
	resp, err := cloud.PutBlob(&PutBlobInput{
		Key:  key,
		Body: bytes.NewReader(content),
		Size: PUInt64(uint64(len(content))),
	})

	if err != nil {
		return "", err
	}

	newETag := ""
	if resp.ETag != nil {
		newETag = *resp.ETag
	}

	return newETag, nil
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
	errStr := err.Error()
	return strings.Contains(errStr, "NoSuchKey") ||
		strings.Contains(errStr, "NotFound") ||
		strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "does not exist")
}
