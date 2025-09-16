// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package params

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dominant-strategies/go-quai/log"
)

var Version CachedVersion

var GitCommit string

type version struct {
	major int
	minor int
	patch int
	meta  string
	full  string
	short string
}

func InitVersion() {
	Version = CachedVersion{}
	Version.load()
}

func readVersionFile() (version, error) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		panic(err)
	}
	if raw[0] != 'v' {
		return version{}, errors.New("version number must start with 'v'")
	}
	full := strings.Replace(string(raw), "\n", "", -1)
	// Take a full version string, e.g. 0.0.0-rc.0
	// and split it into the version number and version metadata (if it has meta).
	// e.g:
	//   - vnum  := 0.0.0
	//   - vmeta := rc.0
	split := bytes.Split(raw, []byte("-"))
	vnum := split[0]
	var vmeta []byte
	if len(split) > 1 {
		vmeta = split[1]
	}
	vnums := bytes.Split(vnum, []byte("."))
	if len(vnums) != 3 {
		return version{}, errors.New("bad version number format")
	}
	major, err := strconv.Atoi(string(vnums[0][1:])) // First byte is 'v'
	if err != nil {
		return version{}, err
	}
	minor, err := strconv.Atoi(string(vnums[1][:]))
	if err != nil {
		return version{}, err
	}
	var patch int
	if len(vmeta) > 0 {
		patch, err = strconv.Atoi(string(vnums[2][:]))
		if err != nil {
			return version{}, err
		}
	} else {
		patch, err = strconv.Atoi(string(vnums[2][:len(vnums[2])-1]))
		if err != nil {
			return version{}, err
		}
	}
	return version{major: major, minor: minor, patch: patch, meta: string(vmeta), full: full, short: string(vnum)}, nil
}

// Version contains software version data parsed from the VERSION file
type CachedVersion struct {
	major atomic.Value // Major version component of the current release
	minor atomic.Value // Minor version component of the current release
	patch atomic.Value // Patch version component of the current release
	meta  atomic.Value // Version metadata (i.e. stable, pre.X, rx.X)
	full  atomic.Value // Full version string (e.g. 0.0.0-rc.0)
	short atomic.Value // Short version string (e.g. 0.0.0)
}

// Load the cached version from the VERSION file
func (v *CachedVersion) load() {
	ver, err := readVersionFile()
	if err != nil {
		log.Global.Fatal("failed to read version file", err)
	}
	v.major.Store(ver.major)
	v.minor.Store(ver.minor)
	v.patch.Store(ver.patch)
	v.meta.Store(ver.meta)
	v.full.Store(ver.full)
	v.short.Store(ver.short)
}

// Major loads the cached major version, or reads it from a file
func (v *CachedVersion) Major() int {
	if num := v.major.Load(); num != nil {
		return num.(int)
	}
	v.load()
	return v.major.Load().(int)
}

// Minor loads the cached minor version, or reads it from a file
func (v *CachedVersion) Minor() int {
	if num := v.minor.Load(); num != nil {
		return num.(int)
	}
	v.load()
	return v.minor.Load().(int)
}

// Patch loads the cached patch version, or reads it from a file
func (v *CachedVersion) Patch() int {
	if num := v.patch.Load(); num != nil {
		return num.(int)
	}
	v.load()
	return v.patch.Load().(int)
}

// Meta loads the cached version metadata, or reads it from a file
// Metadata may be empty if no metadata was provided
func (v *CachedVersion) Meta() string {
	if str := v.meta.Load(); str != nil {
		return str.(string)
	}
	v.load()
	return v.meta.Load().(string)
}

// Full loads the cached full version string, or reads it from a file
func (v *CachedVersion) Full() string {
	if str := v.full.Load(); str != nil {
		return str.(string)
	}
	v.load()
	return v.full.Load().(string)
}

// Full loads the cached full version string, or reads it from a file
func (v *CachedVersion) Short() string {
	if str := v.short.Load(); str != nil {
		return str.(string)
	}
	v.load()
	return v.short.Load().(string)
}

func VersionWithCommit(gitCommit, gitDate string) string {
	vsn := Version.Full()
	if len(gitCommit) >= 8 {
		vsn += "-" + gitCommit[:8]
	}
	if (Version.Meta() != "stable") && (gitDate != "") {
		vsn += "-" + gitDate
	}
	return vsn
}

// ArchiveVersion holds the textual version string used for Quai archives.
// e.g. "1.8.11-dea1ce05" for stable releases, or
//
//	"1.8.13-unstable-21c059b6" for unstable releases
func ArchiveVersion(gitCommit string) string {
	vsn := Version.Short()
	if Version.Meta() != "stable" {
		vsn += "-" + Version.Meta()
	}
	if len(gitCommit) >= 8 {
		vsn += "-" + gitCommit[:8]
	}
	return vsn
}
