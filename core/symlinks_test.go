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
	"testing"

	. "gopkg.in/check.v1"
)

type SymlinksTest struct{}

var _ = Suite(&SymlinksTest{})

func TestSymlinks(t *testing.T) {
	TestingT(t)
}

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

func (s *SymlinksTest) TestGetSymlinksFilePath(t *C) {
	t.Assert(getSymlinksFilePath("", ".symlinks"), Equals, ".symlinks")
	t.Assert(getSymlinksFilePath("dir", ".symlinks"), Equals, "dir/.symlinks")
	t.Assert(getSymlinksFilePath("dir/", ".symlinks"), Equals, "dir/.symlinks")
	t.Assert(getSymlinksFilePath("path/to/dir", ".symlinks"), Equals, "path/to/dir/.symlinks")
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
