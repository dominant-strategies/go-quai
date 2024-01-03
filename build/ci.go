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

//go:build none
// +build none

/*
The ci command is called from Continuous Integration scripts.

Usage: go run build/ci.go <command> <command flags/arguments>

Available commands are:

-  build    [ -arch architecture ] [ -cc compiler ] [ packages... ]                          -- builds packages and executables
-  test       [ -coverage ] [ packages... ]                                                    -- runs the tests
-  lint                                                                                        -- runs certain pre-selected linters

For all commands, -n prevents execution of external programs (dry run mode).
*/
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dominant-strategies/go-quai/internal/build"
)

var (
	// Files that end up in the quai*.zip archive.
	quaiArchiveFiles = []string{
		"COPYING",
		executablePath("quai"),
	}

	// Distros for which packages are created.
	// Note: vivid is unsupported because there is no golang-1.6 package for it.
	// Note: wily is unsupported because it was officially deprecated on Launchpad.
	// Note: yakkety is unsupported because it was officially deprecated on Launchpad.
	// Note: zesty is unsupported because it was officially deprecated on Launchpad.
	// Note: artful is unsupported because it was officially deprecated on Launchpad.
	// Note: cosmic is unsupported because it was officially deprecated on Launchpad.
	// Note: disco is unsupported because it was officially deprecated on Launchpad.
	// Note: eoan is unsupported because it was officially deprecated on Launchpad.
	debDistroGoBoots = map[string]string{
		"trusty":  "golang-1.11",
		"xenial":  "golang-go",
		"bionic":  "golang-go",
		"focal":   "golang-go",
		"groovy":  "golang-go",
		"hirsute": "golang-go",
	}

	debGoBootPaths = map[string]string{
		"golang-1.11": "/usr/lib/go-1.11",
		"golang-go":   "/usr/lib/go",
	}

	// This is the version of go that will be downloaded by
	//
	//     go run ci.go build -dlgo
	dlgoVersion = "1.16.4"
)

var GOBIN, _ = filepath.Abs(filepath.Join("build", "bin"))

func executablePath(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(GOBIN, name)
}

func main() {
	log.SetFlags(log.Lshortfile)

	if _, err := os.Stat(filepath.Join("build", "ci.go")); os.IsNotExist(err) {
		log.Fatal("this script must be run from the root of the repository")
	}
	if len(os.Args) < 2 {
		log.Fatal("need subcommand as first argument")
	}
	switch os.Args[1] {
	case "build":
		doBuild(os.Args[2:])
	case "test":
		doTest(os.Args[2:])
	case "lint":
		doLint(os.Args[2:])
	default:
		log.Fatal("unknown command ", os.Args[1])
	}
}

// Compiling

func doBuild(cmdline []string) {
	var (
		dlgo = flag.Bool("dlgo", false, "Download Go and build with it")
		arch = flag.String("arch", "", "Architecture to cross build for")
		cc   = flag.String("cc", "", "C compiler to cross build with")
	)
	flag.CommandLine.Parse(cmdline)

	// Configure the toolchain.
	tc := build.GoToolchain{GOARCH: *arch, CC: *cc}
	if *dlgo {
		csdb := build.MustLoadChecksums("build/checksums.txt")
		tc.Root = build.DownloadGo(csdb, dlgoVersion)
	}

	// Configure the build.
	env := build.Env()
	gobuild := tc.Go("build", buildFlags(env)...)

	// arm64 CI builders are memory-constrained and can't handle concurrent builds,
	// better disable it. This check isn't the best, it should probably
	// check for something in env instead.
	if env.CI && runtime.GOARCH == "arm64" {
		gobuild.Args = append(gobuild.Args, "-p", "1")
	}

	// We use -trimpath to avoid leaking local paths into the built executables.
	gobuild.Args = append(gobuild.Args, "-trimpath")

	// Show packages during build.
	gobuild.Args = append(gobuild.Args, "-v")

	// Now we choose what we're even building.
	// Default: collect all 'main' packages in cmd/ and build those.
	packages := flag.Args()
	if len(packages) == 0 {
		packages = build.FindMainPackages("./cmd")
	}

	// Do the build!
	for _, pkg := range packages {
		args := make([]string, len(gobuild.Args))
		copy(args, gobuild.Args)
		args = append(args, "-o", executablePath(path.Base(pkg)))
		args = append(args, pkg)
		build.MustRun(&exec.Cmd{Path: gobuild.Path, Args: args, Env: gobuild.Env})
	}
}

// buildFlags returns the go tool flags for building.
func buildFlags(env build.Environment) (flags []string) {
	var ld []string
	if env.Commit != "" {
		ld = append(ld, "-X", "main.gitCommit="+env.Commit)
		ld = append(ld, "-X", "main.gitDate="+env.Date)
	}
	// Strip DWARF on darwin. This used to be required for certain things,
	// and there is no downside to this, so we just keep doing it.
	if runtime.GOOS == "darwin" {
		ld = append(ld, "-s")
	}
	if len(ld) > 0 {
		flags = append(flags, "-ldflags", strings.Join(ld, " "))
	}
	return flags
}

// Running The Tests
//
// "tests" also includes static analysis tools such as vet.

func doTest(cmdline []string) {
	var (
		dlgo     = flag.Bool("dlgo", false, "Download Go and build with it")
		arch     = flag.String("arch", "", "Run tests for given architecture")
		cc       = flag.String("cc", "", "Sets C compiler binary")
		coverage = flag.Bool("coverage", false, "Whether to record code coverage")
		verbose  = flag.Bool("v", false, "Whether to log verbosely")
	)
	flag.CommandLine.Parse(cmdline)

	// Configure the toolchain.
	tc := build.GoToolchain{GOARCH: *arch, CC: *cc}
	if *dlgo {
		csdb := build.MustLoadChecksums("build/checksums.txt")
		tc.Root = build.DownloadGo(csdb, dlgoVersion)
	}
	gotest := tc.Go("test")

	// Test a single package at a time. CI builders are slow
	// and some tests run into timeouts under load.
	gotest.Args = append(gotest.Args, "-p", "1")
	if *coverage {
		gotest.Args = append(gotest.Args, "-covermode=atomic", "-cover")
	}
	if *verbose {
		gotest.Args = append(gotest.Args, "-v")
	}

	packages := []string{"./..."}
	if len(flag.CommandLine.Args()) > 0 {
		packages = flag.CommandLine.Args()
	}
	gotest.Args = append(gotest.Args, packages...)
	build.MustRun(gotest)
}

// doLint runs golangci-lint on requested packages.
func doLint(cmdline []string) {
	var (
		cachedir = flag.String("cachedir", "./build/cache", "directory for caching golangci-lint binary.")
	)
	flag.CommandLine.Parse(cmdline)
	packages := []string{"./..."}
	if len(flag.CommandLine.Args()) > 0 {
		packages = flag.CommandLine.Args()
	}

	linter := downloadLinter(*cachedir)
	lflags := []string{"run", "--config", ".golangci.yml"}
	build.MustRunCommand(linter, append(lflags, packages...)...)
	fmt.Println("You have achieved perfection.")
}

// downloadLinter downloads and unpacks golangci-lint.
func downloadLinter(cachedir string) string {
	const version = "1.49.0"

	csdb := build.MustLoadChecksums("build/checksums.txt")
	base := fmt.Sprintf("golangci-lint-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/golangci/golangci-lint/releases/download/v%s/%s.tar.gz", version, base)
	archivePath := filepath.Join(cachedir, base+".tar.gz")
	if err := csdb.DownloadFile(url, archivePath); err != nil {
		log.Fatal(err)
	}
	if err := build.ExtractArchive(archivePath, cachedir); err != nil {
		log.Fatal(err)
	}
	return filepath.Join(cachedir, base, "golangci-lint")
}
