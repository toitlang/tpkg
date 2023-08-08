// Copyright (C) 2021 Toitware ApS.
//
// This library is free software; you can redistribute it and/or
// modify it under the terms of the GNU Lesser General Public
// License as published by the Free Software Foundation; version
// 2.1 only.
//
// This library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
// Lesser General Public License for more details.
//
// The license can be found in the file `LICENSE` in the top level
// directory of this repository.

package tests

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jstroem/tedi"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/toitlang/tpkg/pkg/compiler"
	"github.com/toitlang/tpkg/pkg/tpkg"
)

const (
	timeout = 60 * time.Second
)

func fix_Context(t *tedi.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.AfterTest(cancel)
	return ctx
}

type toitCmd struct {
	path string
	args []string
	envs map[string]string
}

func (c *toitCmd) RunInDir(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, c.path, append(c.args, args...)...)
	cmd.Dir = dir
	cmd.Env = c.Env()
	return cmd, nil
}

func (c *toitCmd) Env() []string {
	res := os.Environ()
	for k, v := range c.envs {
		res = append(res, k+"="+v)
	}
	return res
}

// The environment variable that gives the toitvm.
const toitvmEnv string = "TOITVM_PATH"

// The environment variable that gives toitc.
const toitcEnv string = "TOITC_PATH"

// The environment variable that gives toitlsp.
const toitlspEnv string = "TOITLSP_PATH"

// The environment variable that gives the tpkg executable. This is a required variable.
const toitpkgEnv string = "TOITPKG_PATH"

// The testing framework updates all gold tests when this environment variable is set.
const updateGoldEnv string = "UPDATE_PKG_GOLD"

// The pattern that will be replaced with the test dir.
// Every asset file will have this pattern replaced.
const testDirPattern string = "<[*TEST_DIR*]>"

// The pattern that will be replaced with the test git url dir.
// Every asset file will have this pattern replaced.
// These URLs are recognized by the tpkg-tool and will treat local paths
// as if they were URLs.
const testDirGitPattern string = "<[*TEST_GIT_DIR*]>"

// The pattern that will be replaced with the escaped test git url dir.
// Every asset file will have this pattern replaced.
// These URLs are recognized by the tpkg-tool and will treat local paths
// as if they were URLs.
const testDirGitEscapePattern string = "<[*TEST_GIT_DIR_ESCAPE*]>"

// The name of the directory that is used for cached entries.
const cacheDir string = "CACHE"

// The name inside the cache directory that is used to download the git packages.
const pkgCacheDir string = "tpkg"

// The name inside the cache directory that is used to download the git registries.
const registryCacheDir string = "tpkg-registries"

// The path inside the project root in which packages are installed by default.
const pkgDir string = ".packages"

const gitTagsDir string = "GIT_TAGS"

type TestDirectory string

type PkgTest struct {
	dir                 string
	overwriteRunDir     string
	t                   *tedi.T
	ctx                 context.Context
	toitpkg             *toitCmd
	toitAnalyze         *toitCmd
	toitExec            *toitCmd
	goldRepls           map[string]string
	pkgDir              string
	cacheDir            string
	pkgCacheDir         string
	registryCacheDir    string
	useDefaultRegistry  bool
	shouldPrintTracking bool
	noAutoSync          bool
	sdkVersion          string
	env                 map[string]string
}

func computeAssetDir(t *tedi.T) string {
	nameParts := strings.Split(t.Name(), "/")
	return filepath.Join(append([]string{"assets", "pkg"}, nameParts[1:]...)...)

}

func computeGitDir(p string) string {
	return tpkg.TestGitPathHost + "/" + filepath.ToSlash(p)
}

func (pt PkgTest) computePathInCache(pkgDir string, version string, p string) string {
	pkgURL := computeGitDir(filepath.Join(pt.dir, pkgDir))
	escaped := compiler.ToURIPath(pkgURL)
	pkgPath := escaped.FilePath()
	return filepath.Join(pt.pkgDir, pkgPath, version, p)
}

func unzip(p string, dir string) error {
	// Open a zip archive for reading.
	r, err := zip.OpenReader(p)
	if err != nil {
		return err
	}
	defer r.Close()

	// Iterate through the files in the archive,
	// printing some of their contents.
	for _, f := range r.File {
		target := filepath.Join(dir, f.Name)
		if f.FileInfo().IsDir() {
			err = os.Mkdir(target, f.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		outFile, err := os.Create(target)
		if err != nil {
			return err
		}
		defer outFile.Close()
		io.Copy(outFile, rc)
	}
	return nil
}

func createGit(t *tedi.T, testDir string, targetDir string, tagsDir string) {
	repository, err := git.PlainInit(targetDir, false)
	require.NoError(t, err)
	err = repository.CreateBranch(&config.Branch{
		Name: "main",
	})
	require.NoError(t, err)
	wt, err := repository.Worktree()
	require.NoError(t, err)
	tags, err := ioutil.ReadDir(tagsDir)
	require.NoError(t, err)
	for _, tagInfo := range tags {
		// Start by deleting all existing files.
		files, err := ioutil.ReadDir(targetDir)
		require.NoError(t, err)
		for _, f := range files {
			if f.Name() == ".git" {
				continue
			}
			err := os.RemoveAll(filepath.Join(targetDir, f.Name()))
			require.NoError(t, err)
		}

		tag := tagInfo.Name()
		// Copy over the content of the tag directory. We will erase it
		// after we have committed and tagged it.
		copyRec(t, testDir, filepath.Join(tagsDir, tag), targetDir)
		err = filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			require.NoError(t, err)
			if path == targetDir {
				return nil
			}
			if path == filepath.Join(targetDir, ".git") {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(targetDir, path)
			require.NoError(t, err)
			_, err = wt.Add(rel)
			require.NoError(t, err)
			return nil
		})
		require.NoError(t, err)
		hash, err := wt.Commit(fmt.Sprintf("Tag: %s", tag), &git.CommitOptions{
			All: true,
			Author: &object.Signature{
				Name:  "Test Committer",
				Email: "not_used@example.com",
				When:  time.Now(),
			},
		})
		require.NoError(t, err)
		_, err = repository.CreateTag(tag, hash, nil)
		require.NoError(t, err)
	}
}

func copyRec(t *tedi.T, testDir string, sourceDir string, targetDir string) {
	// Copy over the content of the asset dir.
	err := filepath.Walk(sourceDir, func(p string, info os.FileInfo, err error) error {
		if p == sourceDir {
			return nil
		}
		require.NoError(t, err)
		rel, err := filepath.Rel(sourceDir, p)
		require.NoError(t, err)
		target := filepath.Join(targetDir, rel)
		if info.IsDir() {
			base := filepath.Base(rel)
			if base == gitTagsDir {
				createGit(t, testDir, filepath.Dir(target), p)
				return filepath.SkipDir
			}
			info, err := os.Stat(target)
			if os.IsNotExist(err) {
				return os.Mkdir(target, 0700)
			}
			require.NoError(t, err)
			if info.IsDir() {
				return nil
			}
			return fmt.Errorf("Can't overwrite shared file with directory")
		}
		// For binary data that we don't want to have in the repository, we
		// allow the data to be zipped. During copying we unzip it.
		// We use the zip mainly for git repositories. These are tests that
		// make sure that we work with repositories that have been created using
		// the git command-line tool.
		if filepath.Ext(p) == ".zip" {
			if strings.HasSuffix(p, "_windows.zip") && runtime.GOOS != "windows" {
				return nil
			}
			if strings.HasSuffix(p, "_posix.zip") && runtime.GOOS == "windows" {
				return nil
			}
			return unzip(p, filepath.Dir(target))
		}
		data, err := ioutil.ReadFile(p)
		if err != nil {
			return err
		}
		testDirCompilerPath := compiler.ToPath(testDir)
		data = bytes.ReplaceAll(data, []byte(testDirPattern), []byte(testDirCompilerPath))
		testDirGitURL := computeGitDir(testDir)
		data = bytes.ReplaceAll(data, []byte(testDirGitPattern), []byte(testDirGitURL))
		escapedTestDirGitURL := string(compiler.ToURIPath(testDirGitURL))
		data = bytes.ReplaceAll(data, []byte(testDirGitEscapePattern), []byte(escapedTestDirGitURL))
		return ioutil.WriteFile(target, data, info.Mode().Perm())
	})
	require.NoError(t, err)
}

func fixtureCreateTestDirectory(t *tedi.T) TestDirectory {
	nameParts := strings.Split(t.Name(), "/")
	name := nameParts[len(nameParts)-1]
	dir, err := ioutil.TempDir("", "pkg-test-"+name)
	require.NoError(t, err)

	// On macos the temp directory is sometimes a symlink, so
	// calling eval-symlinks, makes the output consistent with the output of
	// the analyzer.
	dir, err = filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	t.AfterTest(func() {
		e := os.RemoveAll(string(dir))
		require.NoError(t, e)
	})

	// Shared dir is copied first, so that the asset dirs overwrite
	// the shared files.
	sharedDir := filepath.Join("assets", "pkg", "shared")
	_, err = os.Stat(sharedDir)
	if err != nil {
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	} else {
		copyRec(t, dir, sharedDir, dir)
	}

	assetDir := computeAssetDir(t)
	_, err = os.Stat(assetDir)
	if err != nil {
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	} else {
		copyRec(t, dir, assetDir, dir)
	}

	return TestDirectory(dir)
}

func fixtureCreatePkgTest(ctx context.Context, t *tedi.T, dir TestDirectory) PkgTest {
	absCacheDir := filepath.Join(string(dir), cacheDir)
	absPkgCacheDir := filepath.Join(absCacheDir, pkgCacheDir)
	absPkgDir := filepath.Join(string(dir), pkgDir)
	absRegistryCacheDir := filepath.Join(absCacheDir, registryCacheDir)

	tpkg, _ := os.LookupEnv(toitpkgEnv)
	toitvm, _ := os.LookupEnv(toitvmEnv)
	toitc, _ := os.LookupEnv(toitcEnv)
	toitlsp, _ := os.LookupEnv(toitlspEnv)
	if tpkg == "" {
		log.Fatalf("Missing 'tpkg' path in '%s' environment variable", toitpkgEnv)
	}
	if (toitc == "" && toitlsp != "") || (toitc != "" && toitlsp == "") {
		log.Fatalf("Toitlsp (%s) and toitc (%s) need to given as pairs", toitlspEnv, toitcEnv)
	}
	if toitvm == "" && toitc == "" {
		log.Fatalf("Need either path to VM or toitlsp/toitc in '%s' or '%s/%s' environment variable", toitvmEnv, toitlspEnv, toitcEnv)
	}
	replacements := map[string]string{
		string(dir): "<TEST>",
		tpkg:        "<tpkg>",
	}

	var toitAnalyze *toitCmd
	var toitExec *toitCmd
	if toitvm != "" {
		replacements[toitvm] = "<toitvm>"
		toitExec = &toitCmd{
			path: toitvm,
			args: nil,
		}
	}
	if toitlsp != "" {
		args := []string{"analyze", "--toitc", toitc}
		replacements[toitlsp+" "+strings.Join(args, " ")] = "<analyze>"
		toitAnalyze = &toitCmd{
			path: toitlsp,
			args: args,
		}
	}
	if os.Getenv(updateGoldEnv) != "" {
		if runtime.GOOS != "linux" {
			log.Fatalf("Can only update gold files on Linux")
		}
		if toitExec == nil || toitAnalyze == nil {
			log.Fatalf("Updating gold files requires both the vm and lsp/toitc environment variables")
		}
	}
	return PkgTest{
		dir: string(dir),
		t:   t,
		ctx: ctx,
		toitpkg: &toitCmd{
			path: tpkg,
		},
		toitAnalyze: toitAnalyze,
		toitExec:    toitExec,
		// There will be a few more replacements directly in normalizeGold.
		goldRepls:        replacements,
		pkgDir:           absPkgDir,
		cacheDir:         absCacheDir,
		pkgCacheDir:      absPkgCacheDir,
		registryCacheDir: absRegistryCacheDir,
		env: map[string]string{
			"TOIT_PACKAGE_CACHE_PATHS": absPkgCacheDir,
		},
	}
}

func (pt PkgTest) runToit(args ...string) (string, error) {
	var cmd *exec.Cmd
	var err error
	dir := pt.dir
	if pt.overwriteRunDir != "" {
		dir = pt.overwriteRunDir
	}
	if args[0] == "analyze" {
		cmd, err = pt.toitAnalyze.RunInDir(pt.ctx, dir, args[1:]...)
	} else if args[0] == "exec" {
		cmd, err = pt.toitExec.RunInDir(pt.ctx, dir, args[1:]...)
	} else {
		if pt.noAutoSync {
			args = append([]string{args[0], "--auto-sync=false"}, args[1:]...)
		}
		cmd, err = pt.toitpkg.RunInDir(pt.ctx, dir, args...)
		cmd.Env = append(cmd.Env, "TOIT_CONFIG_FILE="+filepath.Join(pt.dir, "config.yaml"))
		cmd.Env = append(cmd.Env, "TOIT_CACHE_DIR="+filepath.Join(pt.dir, cacheDir))
		cmd.Env = append(cmd.Env, "TOIT_SDK_VERSION= "+pt.sdkVersion)
		if !pt.useDefaultRegistry {
			cmd.Env = append(cmd.Env, "TOIT_NO_DEFAULT_REGISTRY=true")
		}
		if pt.shouldPrintTracking {
			cmd.Env = append(cmd.Env, "TOIT_SHOULD_PRINT_TRACKING=true")
		}
	}
	env := cmd.Env
	for k, v := range pt.env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	require.NoError(pt.t, err)
	out, err := cmd.CombinedOutput()
	result := string(out)
	return result, err
}

func (pt PkgTest) Toit(args ...string) string {
	out, err := pt.runToit(args...)
	require.NoError(pt.t, err, out)
	return out
}

func (pt PkgTest) ToitNegative(args ...string) string {
	out, err := pt.runToit(args...)
	require.Error(pt.t, err, out)
	return out
}

func (pt PkgTest) normalizeGold(gold string) string {
	gitDir := computeGitDir(pt.dir)
	gold = strings.ReplaceAll(gold, gitDir, "<GIT_URL>")
	// When showing lock-file entries we might also see escaped git entries.
	// We can't use a different replacement, as the escaping is dependent on the OS.
	escapedGitURL := string(compiler.ToURIPath(gitDir))
	gold = strings.ReplaceAll(gold, escapedGitURL, "<GIT_URL>")
	gold = strings.ReplaceAll(gold, computeGitDir(pt.dir), "<GIT_URL>")
	for pattern, replacement := range pt.goldRepls {
		gold = strings.ReplaceAll(gold, pattern, replacement)
	}
	if runtime.GOOS == "windows" {
		gold = strings.ReplaceAll(gold, "\r\n", "\n")
		gold = strings.ReplaceAll(gold, "\\", "/")
		testDirCompilerPath := string(compiler.ToPath(pt.dir))
		gold = strings.ReplaceAll(gold, testDirCompilerPath, "<TEST>")
	}
	errorUnderline := regexp.MustCompile(`[\^][~]+`)
	gold = errorUnderline.ReplaceAllString(gold, "^~")
	return gold
}

func diff(old string, new string) string {
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(old),
		B:        difflib.SplitLines(new),
		FromFile: "Old",
		FromDate: "",
		ToFile:   "New",
		ToDate:   "",
		Context:  1,
	})
	return diff
}

func (pt PkgTest) updateGold(name string, newGold string) {
	// We must update the path in the original asset dir and not in the
	// test directory.
	assetDir := computeAssetDir(pt.t)
	goldDir := filepath.Join(assetDir, "gold")
	info, err := os.Stat(goldDir)
	if os.IsNotExist(err) {
		err = os.Mkdir(goldDir, 0755)
	} else {
		require.True(pt.t, info.IsDir())
	}
	require.NoError(pt.t, err)
	goldPath := filepath.Join(goldDir, name+".gold")
	oldBytes, err := ioutil.ReadFile(goldPath)
	if err == nil {
		oldGold := string(oldBytes)
		if string(oldBytes) != newGold {
			fmt.Printf("Updating %s\n%s\n", goldPath, diff(oldGold, newGold))
		}
	} else if os.IsNotExist(err) {
		fmt.Printf("Creating new gold %s with content:\n%s", goldPath, newGold)
	}
	err = ioutil.WriteFile(goldPath, []byte(newGold), 0644)
	require.NoError(pt.t, err)
}

func (pt PkgTest) checkGold(name string, actual string) {
	if os.Getenv(updateGoldEnv) != "" {
		pt.updateGold(name, actual)
		return
	}
	goldPath := filepath.Join(pt.dir, "gold", name+".gold")
	contentBytes, err := ioutil.ReadFile(goldPath)
	require.NoError(pt.t, err)
	gold := string(contentBytes)
	// On windows the gold files come with '\r\n'...
	gold = strings.ReplaceAll(gold, "\r\n", "\n")
	toBeRemoved := "::analyze::"
	if pt.toitExec == nil {
		toBeRemoved = "::exec::"
	}
	lines := strings.Split(gold, "\n")
	filtered := []string{}
	// Filter out all lines that aren't relevant.
	for _, line := range lines {
		if !strings.HasPrefix(line, toBeRemoved) {
			filtered = append(filtered, line)
		}
	}
	filteredGold := strings.Join(filtered, "\n")
	assert.Equal(pt.t, filteredGold, actual)
}

func (pt PkgTest) buildActual(args ...string) string {
	out, err := pt.runToit(args...)
	exitCode := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exitCode = ee.ExitCode()
	} else {
		require.NoError(pt.t, err, out)
	}
	goldOut := fmt.Sprintf("%s\nExit Code: %v\n%s",
		strings.Join(args, " "),
		exitCode,
		out)
	return pt.normalizeGold(goldOut)
}

func (pt PkgTest) GoldToit(name string, commands [][]string) {
	var actuals []string
	for _, command := range commands {
		if strings.HasPrefix(command[0], "//") {
			actuals = append(actuals, strings.Join(command, "\n")+"\n")
		} else if command[0] == "exec" {
			execActual := ""
			// On some platforms we can only run the analyze command, which has a
			// more limited output than the exec command.

			// If we are creating the Gold file, we need to build both outputs.
			// That's why we have a loop here.
			for i := 0; i < 2; i++ {
				if pt.toitExec == nil {
					command[0] = "analyze"
				}
				if i == 1 {
					if os.Getenv(updateGoldEnv) == "" {
						// No need to do it another time.
						break
					}
					if runtime.GOOS != "linux" {
						log.Fatalf("Can only update gold files on Linux")
					}
					command[0] = "analyze"
				}

				actual := pt.buildActual(command...)
				lines := strings.Split(actual, "\n")
				for i, line := range lines {
					lines[i] = "::" + command[0] + ":: " + line + "\n"
				}
				execActual += strings.Join(lines, "")
			}
			actuals = append(actuals, execActual)
		} else {
			actual := pt.buildActual(command...)
			actuals = append(actuals, actual)
		}
	}
	combined := strings.Join(actuals, "===================\n")
	pt.checkGold(name, combined)
}

func deleteRegCache(t *tedi.T, pt PkgTest, regPath string) {
	// Delete the registry cache.
	escapedRegistry := compiler.FilePathToURIPath(regPath).FilePath()
	registryPath := filepath.Join(pt.registryCacheDir, escapedRegistry)
	assert.DirExists(t, registryPath)
	err := os.RemoveAll(registryPath)
	assert.NoError(t, err)
}

func test_toitPkg(t *tedi.T) {
	t.Parallel()

	t.Run("HelloWorld", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("hello", [][]string{
			{"exec", "hello.toit"},
		})
	})

	t.Run("GitTagDir", func(t *tedi.T, pt PkgTest) {
		// Just a simple check that our test-setup function works.
		gitDir := filepath.Join(pt.dir, "git_dir")
		dirInFiles := string(compiler.ToPath(pt.dir + "/git_dir"))
		repository, err := git.PlainOpen(gitDir)
		require.NoError(t, err)
		wt, err := repository.Worktree()
		require.NoError(t, err)
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewTagReferenceName("1.0.0"),
		})
		require.NoError(t, err)
		data, err := ioutil.ReadFile(filepath.Join(gitDir, "a"))
		require.NoError(t, err)
		dataStr := strings.ReplaceAll(string(data), "\r\n", "\n")
		// Notice that we implicitly check the correct `<[*TEST_DIR*>]` replacement.
		assert.Equal(t, dirInFiles+"/a 1.0.0\n", dataStr)
		data, err = ioutil.ReadFile(filepath.Join(gitDir, "b"))
		require.NoError(t, err)
		dataStr = strings.ReplaceAll(string(data), "\r\n", "\n")
		assert.Equal(t, dirInFiles+"/b 1.0.0\n", dataStr)
		_, err = os.Stat(filepath.Join(gitDir, "c"))
		assert.True(t, os.IsNotExist(err))

		// Now checkout tag 2.0.0 and verify that the files changed and that
		// the 'b' file disappeared.
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewTagReferenceName("2.0.0"),
		})
		require.NoError(t, err)
		data, err = ioutil.ReadFile(filepath.Join(gitDir, "a"))
		require.NoError(t, err)
		dataStr = strings.ReplaceAll(string(data), "\r\n", "\n")
		assert.Equal(t, dirInFiles+"/a 2.0.0\n", dataStr)
		_, err = os.Stat(filepath.Join(gitDir, "b"))
		assert.True(t, os.IsNotExist(err))
		data, err = ioutil.ReadFile(filepath.Join(gitDir, "c"))
		require.NoError(t, err)
		dataStr = strings.ReplaceAll(string(data), "\r\n", "\n")
		assert.Equal(t, dirInFiles+"/c 2.0.0\n", dataStr)
	})

	t.Run("Install1", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("install", [][]string{
			{"exec", "main.toit"},
			{"pkg", "install", "--local", "pkg"},
			{"exec", "main.toit"},
			{"// Install with a prefix."},
			{"pkg", "install", "--local", "--prefix=prepkg", "pkg2"},
			{"exec", "main2.toit"},
			{"// Installing again yields an error."},
			{"pkg", "install", "--local", "pkg"},
			{"// Installing a package where the directory name is not the package name."},
			{"pkg", "install", "--local", "pkg3"},
			{"exec", "main3.toit"},
		})
		pt.GoldToit("install_non_existing", [][]string{
			{"pkg", "install", "--local", "non-existing"},
		})
		pt.GoldToit("install_file", [][]string{
			{"pkg", "install", "--local", "main.toit"},
		})
		pt.GoldToit("install_existing_prefix", [][]string{
			{"pkg", "install", "--local", "--prefix=pkg", "pkg2"},
		})
		pt.GoldToit("install_non_existing_git", [][]string{
			{"pkg", "install", "some_pkg"},
		})
		pt.GoldToit("install_missing_yaml", [][]string{
			{"pkg", "install", "--local", "pkg_missing_yaml"},
		})
	})

	t.Run("List1", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("list", [][]string{
			{"pkg", "list", "list_registry"},
		})
		pt.GoldToit("list--verbose", [][]string{
			{"pkg", "list", "--verbose", "registry"},
		})
		pt.GoldToit("bad", [][]string{
			{"pkg", "list", "bad_registry"},
		})
		pt.GoldToit("bad2", [][]string{
			{"pkg", "list", "bad_registry2"},
		})
		pt.GoldToit("bad3", [][]string{
			{"pkg", "list", "bad_registry3"},
		})
		pt.GoldToit("bad4", [][]string{
			{"pkg", "list", "bad_registry4"},
		})
		pt.GoldToit("bad5", [][]string{
			{"pkg", "list", "bad_registry5"},
		})
	})

	t.Run("Registry1", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry")
		pt.GoldToit("registry", [][]string{
			{"// In a fresh configuration we don't expect to see any registry."},
			{"pkg", "registry", "list"},
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"pkg", "registry", "list"},
			{"pkg", "list"},
			{"// Note that the second registry is added with a relative path",
				"// But that the list below shows it with an absolute path"},
			{"pkg", "registry", "add", "--local", "test-reg2", "registry2"},
			{"pkg", "registry", "list"},
			{"pkg", "list"},
			{"pkg", "registry", "add", "--local", "bad-reg", "bad_registry"},
			{"// It's OK to add the same registry with the same name again"},
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"// It's an error to add a registry with an existing name but a different path"},
			{"pkg", "registry", "add", "--local", "test-reg", "registry2"},
		})
	})

	t.Run("Search1", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry")
		regPath2 := filepath.Join(pt.dir, "registry2")
		pt.GoldToit("search", [][]string{
			{"// Since there is no registry, we shouldn't find any package."},
			{"pkg", "search", "foo"},
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"// Search should find packages now."},
			{"pkg", "search", "foo"},
			{"pkg", "search", "--verbose", "foo"},
			{"pkg", "search", "Foo-Desc"},
			{"pkg", "search", "bar"},
			{"pkg", "search", "sub"},
			{"// The gee package doesn't exist in this registry."},
			{"pkg", "search", "gee"},
			{"// Search also finds things in descriptions."},
			{"pkg", "search", "foo-desc"},
			{"pkg", "search", "bar-desc"},
			{"pkg", "search", "bAr-dEsc"},
			{"pkg", "search", "desc"},
			{"// Search also finds things in the URL."},
			{"pkg", "search", "foo_git"},
			{"pkg", "search", "bar_git"},
			{"pkg", "registry", "add", "--local", "test-reg2", regPath2},
			{"// The new foo package has a higher version and shadows the other one."},
			{"pkg", "search", "foo"},
			{"// The gee package is now visible too."},
			{"pkg", "search", "gee"},
			{"// Works with bad case and subset"},
			{"pkg", "search", "Ee"},
			{"// Install doesn't work with subsets"},
			{"pkg", "install", "Ee"},
			{"// The bar and sub package didn't change"},
			{"pkg", "search", "bar"},
			{"pkg", "search", "sub"},
			{"// Find all packages:"},
			{"pkg", "search", ""},
		})
	})

	t.Run("GitPackage", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("git package search", [][]string{
			{"// Execution should fail, as the package is not installed yet"},
			{"exec", "main.toit"},
			{"// Install packages from the registry"},
			{"pkg", "registry", "add", "--local", "test-reg", "registry"},
			{"pkg", "install", "foo"},
			{"pkg", "install", "bar"},
			{"// Execution should succeed now"},
			{"exec", "main.toit"},
			{"// Execution should fail, as the prefixes are not yet known"},
			{"exec", "main2.toit"},
			{"pkg", "install", "--prefix=pre1", "foo"},
			{"pkg", "install", "--prefix=pre2", "bar"},
			{"// Execution should succeed now"},
			{"exec", "main2.toit"},
		})

		pt.GoldToit("bad-pkg search", [][]string{
			{"// Add a registry, so that we have conflicts"},
			{"pkg", "registry", "add", "--local", "test-reg2", "registry2"},
			{"pkg", "install", "--prefix=pre3", "foo"},
		})

		pt.GoldToit("package.lock", [][]string{
			{"pkg", "lockfile"},
		})

		readmePath := filepath.Join(pt.pkgDir, "README.md")
		assert.FileExists(t, readmePath)

		fooFile := pt.computePathInCache("foo_git", "1.2.3", "package.yaml")
		fooStat, err := os.Stat(fooFile)
		assert.NoError(t, err)
		assert.Contains(t, fooStat.Mode().String(), "r")
		assert.NotContains(t, fooStat.Mode().String(), "w")
	})

	t.Run("InstallInPackage", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "init"},
			{"pkg", "registry", "add", "--local", "test-reg", "registry"},
			{"pkg", "install", "foo"},
			{"pkg", "install", "bar"},
			{"pkg", "lockfile"},
			{"pkg", "packagefile"},
		})
	})

	t.Run("Download1", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("install", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", "registry"},
			{"pkg", "install", "foo"},
			{"pkg", "install", "bar"},
			{"pkg", "install", "--local", "target"},
			{"exec", "main.toit"},
		})

		fooVersion := "1.2.3"
		fooPath := pt.computePathInCache("foo_git", fooVersion, "")
		info, err := os.Stat(fooPath)
		require.NoError(t, err)
		require.True(t, info.IsDir())
		err = os.RemoveAll(fooPath)
		require.NoError(t, err)

		barVersion := "2.0.1"
		barPath := pt.computePathInCache("bar_git", barVersion, "")
		info, err = os.Stat(barPath)
		require.NoError(t, err)
		require.True(t, info.IsDir())
		err = os.RemoveAll(barPath)
		require.NoError(t, err)

		pt.GoldToit("fail", [][]string{
			{"exec", "main.toit"},
		})
		pt.GoldToit("download", [][]string{
			{"pkg", "download"},
		})
		pt.GoldToit("exec after download", [][]string{
			{"exec", "main.toit"},
		})

		// Ensure that the directories are back.
		info, err = os.Stat(fooPath)
		require.NoError(t, err)
		require.True(t, info.IsDir())

		info, err = os.Stat(barPath)
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})

	t.Run("Install2", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test", [][]string{
			{"// No package installed yet."},
			{"exec", "main.toit"},
			{"// Add registry so we can find packages."},
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"// Just 'install' doesn't add the missing dependencies."},
			{"pkg", "install"},
			{"pkg", "lockfile"},
			{"// With '--recompute' we get the missing dependencies."},
			{"pkg", "install", "--recompute"},
			{"// Should work now."},
			{"exec", "main.toit"},
		})
	})

	t.Run("Install3", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"// Nothing to 'install'"},
			{"pkg", "install"},
		})
	})

	t.Run("Install4", func(t *tedi.T, pt PkgTest) {
		for i := 0; i < 2; i++ {
			if i == 1 {
				// Second round. Remove the package and lock file.
				assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.lock")))
				assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.yaml")))
			}
			regPath := filepath.Join(pt.dir, "registry_git_pkgs")
			pt.GoldToit(fmt.Sprintf("test-%d", i), [][]string{
				{"// No package installed yet."},
				{"exec", "main.toit"},
				{"exec", "main2.toit"},
				{"// Add registry so we can find packages."},
				{"pkg", "registry", "add", "--local", "test-reg", regPath},
				{"// Install pkg4 for 'main.toit', creating/updating a lock file."},
				{"pkg", "install", "pkg4", "--prefix=pkg4_pre"},
				{"// main.toit should work now."},
				{"exec", "main.toit"},
				{"pkg", "install", "pkg1"},
				{"// main2.toit should also work now."},
				{"exec", "main2.toit"},
			})
		}
	})

	t.Run("Install5", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_git_pkgs")
		regPath2 := filepath.Join(pt.dir, "registry_ambiguous")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"pkg", "registry", "add", "--local", "test-reg2", regPath2},
			{"// Ambiguous pkg1"},
			{"pkg", "install", "pkg1"},
			{"// Disambiguate by giving full URL."},
			{"pkg", "install", computeGitDir(filepath.Join(pt.dir, "git_pkgs", "pkg1"))},
			{"// Ambiguous pkg2"},
			{"pkg", "search", "--verbose", "pkg2"},
			{"// Disambiguate by giving full URL even though that's the suffix of the longer one."},
			{"pkg", "install", computeGitDir(filepath.Join(pt.dir, "git_pkgs", "pkg2"))},
			{"// Ambiguous 'ambiguous'"},
			{"pkg", "search", "--verbose", "ambiguous"},
			{"// Need to add more segments to disambiguate."},
			{"pkg", "install", "b/c/d/ambiguous"},
			{"// Will still yield an error (because we don't have the package),",
				"// but it's a different one"},
			{"pkg", "install", "a/b/c/d/ambiguous"},
		})
	})

	t.Run("InstallVersion", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_many_versions")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath},
			{"pkg", "list"},
			{"pkg", "install", "many"},
			{"pkg", "install", "many@99"},
			{"pkg", "install", "many@1"},
			{"pkg", "install", "--prefix=foo", "many@1.0"},
			{"pkg", "lockfile"},
			{"pkg", "packagefile"},
			{"pkg", "install", "--prefix=gee", "many@1"},
			{"pkg", "lockfile"},
			{"pkg", "packagefile"},
			{"pkg", "install", "--prefix=bad1", "many@"},
			{"pkg", "install", "--prefix=bad2", "many@not_a-version"},
		})
		for _, version := range []string{
			"1",
			"1.1",
			"2",
			"2.3",
			"2.3.5",
		} {
			// Remove the lock and package file.
			assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.lock")))
			assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.yaml")))
			pt.GoldToit("test-"+version, [][]string{
				{"pkg", "install", "many@" + version},
				{"pkg", "lockfile"},
				{"pkg", "packagefile"},
			})
		}
	})

	t.Run("Install-bad", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test", [][]string{
			{"// Add registry so we can find packages."},
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"// Prefix must be used with package name."},
			{"pkg", "install", "--prefix=foo"},
			{"// Path must be used with path."},
			{"pkg", "install", "--local"},
			{"// Prefix must be valid."},
			{"pkg", "install", "--prefix", "invalid prefix", "pkg2"},
		})
	})

	t.Run("InstallChangeName", func(pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_change")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"pkg", "install", "foo@1.1"},
			{"pkg", "install", "bar"},
			{"pkg", "uninstall", "other_name"},
			{"pkg", "uninstall", "bar"},
			{"pkg", "install", "foo"},
		})
	})

	t.Run("InstallAbsLocal", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "install"},
			{"pkg", "lockfile"},
		})
	})

	t.Run("RegistrySkipHidden", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "reg_with_hidden")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath},
			{"// Should be empty and ignore the yaml file in the hidden folder"},
			{"pkg", "registry", "list"},
		})
	})

	t.Run("GitRegistry1", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test", [][]string{
			{"// Add git registry"},
			{"pkg", "registry", "add", "test-reg", regPath},
			{"pkg", "install", "pkg1"},
			{"exec", "main.toit"},
			{"// Adding it again has no effect"},
			{"pkg", "registry", "add", "test-reg", regPath},
		})
	})

	t.Run("GitRegistryNotSynced", func(t *tedi.T, pt PkgTest) {
		regPath := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test-1", [][]string{
			{"// Add git registry"},
			{"pkg", "registry", "add", "test-reg", regPath},
		})

		deleteRegCache(t, pt, regPath)

		pt.GoldToit("test-autosync-list", [][]string{
			{"pkg", "list"},
		})

		deleteRegCache(t, pt, regPath)
		pt.GoldToit("test-autosync-install", [][]string{
			{"pkg", "install"},
		})

		deleteRegCache(t, pt, regPath)
		pt.GoldToit("test-autosync-install2", [][]string{
			{"pkg", "install", "pkg1"},
		})

		deleteRegCache(t, pt, regPath)

		pt.noAutoSync = true
		pt.GoldToit("test-no-autosync", [][]string{
			{"// Without sync there shouldn't be any packages"},
			{"pkg", "list"},
			{"// Install should, however, still work"},
			{"pkg", "install"},
			{"exec", "test.toit"},
			{"// Error is expected now."},
			{"pkg", "install", "pkg1"},
		})
	})

	t.Run("GitRegistrySync", func(t *tedi.T, pt PkgTest) {
		for i := 0; i < 2; i++ {
			regPath := filepath.Join(pt.dir, "registry_git_pkgs")

			suffix := "-autosync"
			yamlFile := "pkg_test.yaml"
			if i == 1 {
				pt.GoldToit("test-reg-rm", [][]string{
					{"pkg", "registry", "remove", "test-reg"},
				})

				deleteRegCache(t, pt, regPath)

				// No autosync for the second pass.
				suffix = "-no-autosync"
				yamlFile = "pkg_test2.yaml"
				pt.noAutoSync = true
			}

			pt.GoldToit("test-1"+suffix, [][]string{
				{"pkg", "registry", "add", "test-reg", regPath},
				{"pkg", "list"},
			})

			data, err := ioutil.ReadFile(filepath.Join(pt.dir, yamlFile))
			require.NoError(t, err)
			pkgTestSpecPath := filepath.Join(regPath, yamlFile)
			err = ioutil.WriteFile(pkgTestSpecPath, data, 0644)
			require.NoError(t, err)

			repository, err := git.PlainOpen(regPath)
			require.NoError(t, err)
			wt, err := repository.Worktree()
			require.NoError(t, err)

			rel, err := filepath.Rel(regPath, pkgTestSpecPath)
			require.NoError(t, err)
			_, err = wt.Add(rel)
			require.NoError(t, err)
			_, err = wt.Commit("Add pkg_test.yaml", &git.CommitOptions{
				All: true,
				Author: &object.Signature{
					Name:  "Test Committer",
					Email: "not_used@example.com",
					When:  time.Now(),
				},
			})
			require.NoError(t, err)

			pt.GoldToit("test-2"+suffix, [][]string{
				{"pkg", "list"},
				{"pkg", "registry", "sync"},
				{"pkg", "list"},
				{"pkg", "registry", "sync"},
			})
		}
	})

	t.Run("GitRegistrySyncBad", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "sync", "bad"},
		})
	})

	t.Run("Preferred", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		regPath2 := filepath.Join(pt.dir, "registry")
		regPath3 := filepath.Join(pt.dir, "registry_git_pkgs_newer_versions")
		pt.GoldToit("test-1", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "list"},
			{"pkg", "install", "pkg1"},
			{"pkg", "lockfile"},
			{"pkg", "registry", "add", "test-reg3", regPath3},
			{"pkg", "list"},
			{"pkg", "registry", "add", "--local", "test-reg2", regPath2},
			{"pkg", "install", "foo"},
			{"// Installing foo did not change the versions of the existing packages"},
			{"pkg", "lockfile"},
		})

		// Remove the lock and package file.
		assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.lock")))
		assert.NoError(t, os.Remove(filepath.Join(pt.dir, "package.yaml")))

		pt.GoldToit("test-2", [][]string{
			{"pkg", "install", "pkg1"},
			{"// Now we have the newer versions"},
			{"pkg", "lockfile"},
		})
	})

	t.Run("Update", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		regPath2 := filepath.Join(pt.dir, "registry_git_pkgs_newer_versions")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "list"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
			{"pkg", "lockfile"},
			{"pkg", "packagefile"},
			{"pkg", "registry", "add", "test-reg3", regPath2},
			{"pkg", "update"},
			{"pkg", "lockfile"},
			{"pkg", "packagefile"},
		})
	})

	t.Run("Init", func(t *tedi.T, pt PkgTest) {
		lockPath := filepath.Join(pt.dir, "package.lock")
		pkgPath := filepath.Join(pt.dir, "package.yaml")

		assert.NoFileExists(t, pkgPath)
		assert.NoFileExists(t, lockPath)

		pt.GoldToit("init1", [][]string{
			{"pkg", "init"},
		})

		assert.FileExists(t, pkgPath)
		assert.FileExists(t, lockPath)

		err := os.Remove(pkgPath)
		assert.NoError(t, err)
		err = os.Remove(lockPath)
		assert.NoError(t, err)

		pt.GoldToit("already_init", [][]string{
			{"pkg", "init"},
			{"pkg", "init"},
		})

		assert.FileExists(t, pkgPath)
		assert.FileExists(t, lockPath)

		// Make sure the generated lock file can be used.
		pt.GoldToit("app-install", [][]string{
			{"exec", "main.toit"},
			{"pkg", "install", "--local", "pkg"},
			{"exec", "main2.toit"},
		})

		other := filepath.Join(pt.dir, "other")
		err = os.Mkdir(other, 0700)
		assert.NoError(t, err)

		pt.GoldToit("initOther", [][]string{
			{"pkg", "init", "--project-root=" + other},
		})

		assert.FileExists(t, filepath.Join(other, "package.yaml"))
		assert.FileExists(t, filepath.Join(other, "package.lock"))
	})

	t.Run("InstallForPackage", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")

		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
			{"pkg", "lockfile"},
		})

		yamlPath := filepath.Join(pt.dir, "package.yaml")
		assert.FileExists(t, yamlPath)
		lockPath := filepath.Join(pt.dir, "package.lock")
		assert.FileExists(t, lockPath)

		err := os.Remove(lockPath)
		assert.NoError(t, err)

		pt.GoldToit("test2", [][]string{
			{"pkg", "install"},
			{"pkg", "lockfile"},
		})
	})

	t.Run("InstallInCustomPackageDir", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		customInstallPath := filepath.Join(pt.dir, "my_packages")
		pt.env["TOIT_PACKAGE_INSTALL_PATH"] = customInstallPath
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
			{"pkg", "lockfile"},
		})

		_, err := os.Stat(filepath.Join(pt.dir, ".packages"))
		assert.True(t, os.IsNotExist(err))

		stat, err := os.Stat(customInstallPath)
		require.NoError(t, err)
		assert.True(t, stat.IsDir())
	})

	t.Run("MoreLock", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")

		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
		})

		yamlPath := filepath.Join(pt.dir, "package.yaml")
		err := ioutil.WriteFile(yamlPath, []byte{}, 0644)
		assert.NoError(t, err)

		pt.GoldToit("test2", [][]string{
			{"// Should error, as the lock file has more entries."},
			{"pkg", "install", "pkg3"},
		})
	})

	t.Run("DefaultRegistry", func(t *tedi.T, pt PkgTest) {
		pt.useDefaultRegistry = true
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "list"},
			{"pkg", "sync"},
			{"pkg", "init"},
			{"pkg", "install", "github.com/toitware/toit-morse"},
			{"pkg", "registry", "add", "toit", "github.com/toitware/registry"},
		})
		configPath := filepath.Join(pt.dir, "config.yaml")
		data, err := ioutil.ReadFile(configPath)
		assert.NoError(t, err)
		assert.Contains(t, string(data), "toitware/registry")
	})

	t.Run("RemoveRegistry", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		regPath2 := filepath.Join(pt.dir, "registry")

		pt.useDefaultRegistry = true
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "list"},
			{"pkg", "registry", "remove", "toit"},
			{"pkg", "registry", "list"},
			{"pkg", "registry", "add", "test-reg1", regPath1},
			{"pkg", "registry", "add", "--local", "test-reg2", regPath2},
			{"pkg", "registry", "list"},
			{"pkg", "registry", "remove", "non-existant"},
			{"pkg", "registry", "remove", "test-reg1"},
			{"pkg", "registry", "list"},
			{"pkg", "registry", "remove", "test-reg2"},
			{"pkg", "registry", "list"},
		})
	})

	t.Run("Scrape", func(t *tedi.T, pt PkgTest) {
		dirs, err := ioutil.ReadDir(filepath.Join(pt.dir, "pkg_dirs"))
		assert.NoError(t, err)
		for _, entry := range dirs {
			if !entry.IsDir() {
				continue
			}
			test := entry.Name()
			if test == "gold" {
				continue
			}
			t.Run(test, func() {
				p := filepath.Join("pkg_dirs", test)
				pt.GoldToit(test, [][]string{
					{"pkg", "describe", p},
					{"pkg", "describe", "--verbose", p},
				})
			})
		}
		t.Run("local_path", func() {
			p := filepath.Join("local_path")
			pt.GoldToit("local_path", [][]string{
				{"pkg", "describe", p},
				{"pkg", "describe", "--verbose", p},
				{"pkg", "describe", "--allow-local-deps", p},
				{"pkg", "describe", "--disallow-local-deps", p},
				{"pkg", "describe", "--allow-local-deps", "--disallow-local-deps", p},
			})
		})
	})

	t.Run("ScrapeGit", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("morse", [][]string{
			{"pkg", "describe", "github.com/toitware/toit-morse", "1.0.6"},
		})
		pt.GoldToit("https_morse", [][]string{
			{"pkg", "describe", "https://github.com/toitware/toit-morse", "1.0.6"},
		})

		pt.GoldToit("not_found", [][]string{
			{"pkg", "describe", "https://toit.io/testing/not_exist", "1.0.0"},
		})

		pt.GoldToit("bad_version", [][]string{
			{"pkg", "describe", "https://github.com/toitware/toit-morse", "bad-version"},
		})

		pt.GoldToit("deep", [][]string{
			{"pkg", "describe", "https://github.com/toitware/test-pkg.git/foo", "1.0.0"},
		})

		pt.GoldToit("local_dep", [][]string{
			{"pkg", "describe", "https://github.com/toitware/test-pkg.git/local_dep", "1.0.0"},
			{"pkg", "describe", "--allow-local-deps", "https://github.com/toitware/test-pkg.git/local_dep", "1.0.0"},
		})

		outDir := filepath.Join(pt.dir, "out")
		pt.GoldToit("write", [][]string{
			{"pkg", "describe", ".", "--out-dir=foo"},
			{"pkg", "describe", "--out-dir=foo"},
			{"pkg", "describe", "https://github.com/toitware/toit-morse", "1.0.6", "--out-dir=" + outDir},
		})
		descPath := filepath.Join(pt.dir, "out", "packages", "github.com", "toitware", "toit-morse", "1.0.6", "desc.yaml")
		assert.FileExists(t, descPath)
		_, err := ioutil.ReadFile(descPath)
		assert.NoError(t, err)
	})

	t.Run("RequireProjectRoot", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("pre", [][]string{
			{"pkg", "init"},
		})
		nestedDir := filepath.Join(pt.dir, "nested")
		err := os.Mkdir(nestedDir, 0700)
		assert.NoError(t, err)
		pt.overwriteRunDir = nestedDir
		// Remove all single-quotes from the gold file.
		// The output of this test is non-deterministic depending on the path in which
		// the test is run. Sometimes arguments are quoted, sometimes they aren't.
		// For simplicity just remove the quotes all the time.
		pt.goldRepls["'"] = ""
		pt.GoldToit("post", [][]string{
			{"pkg", "install", "github.com/toitware/toit-morse"},
		})
	})

	t.Run("DeepPackage", func(t *tedi.T, pt PkgTest) {
		registryDir := filepath.Join(pt.dir, "nested_registry")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "deep", registryDir},
			{"pkg", "describe", "--out-dir=" + registryDir, "github.com/toitware/test-pkg.git/foo", "1.0.0"},
			{"pkg", "describe", "--out-dir=" + registryDir, "github.com/toitware/test-pkg.git/foo", "2.3.0"},
			{"pkg", "describe", "--out-dir=" + registryDir, "github.com/toitware/test-pkg.git/bar/gee", "1.0.1"},
			{"pkg", "list"},
			{"pkg", "install"},
			{"exec", "test.toit"},
		})
	})

	t.Run("GitHash", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "install"},
			{"exec", "main.toit"},
		})
	})

	t.Run("Uninstall", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
			{"pkg", "lockfile"},
			{"pkg", "uninstall", "pkg1"},
			{"pkg", "uninstall", "pkg1"},
			{"pkg", "uninstall", "pkg2"},
			{"pkg", "lockfile"},
		})
	})

	t.Run("Clean", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.GoldToit("test1", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			{"pkg", "install", "pkg1"},
			{"pkg", "install", "pkg2"},
		})
		pkg1Path := pt.computePathInCache(filepath.Join("git_pkgs", "pkg1"), "1.0.0", "")
		pkg2Path := pt.computePathInCache(filepath.Join("git_pkgs", "pkg2"), "2.4.2", "")
		pkg3Path := pt.computePathInCache(filepath.Join("git_pkgs", "pkg3"), "3.1.2", "")
		assert.DirExists(t, pkg1Path)
		assert.DirExists(t, pkg2Path)
		assert.DirExists(t, pkg3Path)

		pt.GoldToit("test2", [][]string{
			{"pkg", "uninstall", "pkg1"},
			{"pkg", "clean"},
		})

		assert.NoDirExists(t, pkg1Path)
		assert.DirExists(t, pkg2Path)
		assert.DirExists(t, pkg3Path)

		pt.GoldToit("test3", [][]string{
			{"pkg", "uninstall", "pkg2"},
			{"pkg", "clean"},
		})

		assert.NoDirExists(t, pkg1Path)
		assert.NoDirExists(t, pkg2Path)
		assert.NoDirExists(t, pkg3Path)
	})

	t.Run("Tracking", func(t *tedi.T, pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_git_pkgs")
		pt.env["TOIT_SHOULD_PRINT_TRACKING"] = "true"
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
			{"pkg", "init"},
			// Note that the order of downloading packages is not deterministic.
			// For simplicity we therefore install a package without dependencies.
			{"pkg", "install", "pkg3"},
			{"pkg", "install"},
			{"pkg", "install", "--recompute"},
			{"pkg", "search", "pkg"},
			{"pkg", "registry", "remove", "test-reg"},
			{"pkg", "describe", "github.com/toitware/toit-morse", "v1.0.6"},
		})
	})

	t.Run("GitNative", func(t *tedi.T, pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "install"},
			{"exec", "main.toit"},
		})
	})

	t.Run("SDKVersion", func(pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath1},
			{"pkg", "list", "--verbose"},
			{"pkg", "init"},
		})
		pt.sdkVersion = "0.0.0"
		pt.GoldToit("test2", [][]string{
			{"// sdkVersion = 0.0.0"},
			{"pkg", "install", "foo"},
		})
		pt.sdkVersion = ""
		pt.GoldToit("test3", [][]string{
			{"pkg", "install", "foo"},
			{"exec", "main.toit"},
			{"pkg", "uninstall", "foo"},
		})
		pt.sdkVersion = "0.1.10"
		pt.GoldToit("test4", [][]string{
			{"// sdkVersion = 0.1.10"},
			{"pkg", "install", "foo"},
		})
		pt.sdkVersion = ""
		pt.GoldToit("test5", [][]string{
			{"exec", "main.toit"},
			{"pkg", "uninstall", "foo"},
			{"pkg", "install", "foo"},
			{"exec", "main.toit"},
		})
	})

	t.Run("SDKVersion2", func(pt PkgTest) {
		pt.GoldToit("test", [][]string{
			{"pkg", "install"},
			{"pkg", "lockfile"},
		})
	})

	t.Run("SDKVersion3", func(pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry")
		pt.sdkVersion = "0.1.10"
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath1},
			{"// sdkVersion = 0.1.10"},
			{"pkg", "install", "foo"},
			{"pkg", "lockfile"},
		})
		pt.sdkVersion = ""
		// A new 'install' doesn't change the selected lock files, even though
		// our version is now better.
		pt.GoldToit("test2", [][]string{
			{"// sdkVersion = "},
			{"pkg", "install"},
			{"pkg", "lockfile"},
		})
		// Modifying the version constraint in the package.spec is copied to the
		// lock file.
		packageSpecPath := filepath.Join(pt.dir, "package.yaml")
		data, err := ioutil.ReadFile(packageSpecPath)
		assert.NoError(t, err)
		str := string(data) + `
environment:
  sdk: ^1.20.0
`
		err = ioutil.WriteFile(packageSpecPath, []byte(str), 0644)
		assert.NoError(t, err)
		pt.GoldToit("test3", [][]string{
			{"// sdkVersion = "},
			{"pkg", "install"},
			{"// Lockfile now has 1.20 SDK constraint."},
			{"pkg", "lockfile"},
		})
	})

	t.Run("SDKVersionFlag", func(pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry")
		pt.GoldToit("test", [][]string{
			{"pkg", "registry", "add", "--local", "test-reg", regPath1},
			{"pkg", "list", "--verbose"},
			{"pkg", "init"},
		})
		pt.GoldToit("test2", [][]string{
			{"pkg", "--sdk-version", "v0.0.0", "install", "foo"},
		})
		pt.GoldToit("test3", [][]string{
			{"pkg", "install", "foo"},
			{"exec", "main.toit"},
			{"pkg", "uninstall", "foo"},
		})
		pt.GoldToit("test4", [][]string{
			{"pkg", "--sdk-version", "v0.1.10", "install", "foo"},
		})
		pt.GoldToit("test5", [][]string{
			{"exec", "main.toit"},
			{"pkg", "uninstall", "foo"},
			{"pkg", "install", "foo"},
			{"exec", "main.toit"},
		})
	})

	t.Run("ParallelSync", func(pt PkgTest) {
		regPath1 := filepath.Join(pt.dir, "registry_parallel")
		pt.GoldToit("add_registry", [][]string{
			{"pkg", "registry", "add", "test-reg", regPath1},
		})
		deleteRegCache(t, pt, regPath1)

		wg := sync.WaitGroup{}
		for i := 0; i < 3; i++ {
			current := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				pt.GoldToit(fmt.Sprint("test", current), [][]string{
					{"pkg", "sync"},
				})
			}()
		}
		wg.Wait()
	})
}
