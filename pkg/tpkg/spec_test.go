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

package tpkg

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/toitlang/tpkg/pkg/compiler"
)

type testUI struct {
	messages []string
}

func (ui *testUI) ReportError(format string, a ...interface{}) error {
	ui.messages = append(ui.messages, fmt.Sprintf("Error: "+format, a...))
	return ErrAlreadyReported
}

func (ui *testUI) ReportWarning(format string, a ...interface{}) {
	ui.messages = append(ui.messages, fmt.Sprintf("Warning: "+format, a...))
}

func (ui *testUI) ReportInfo(format string, a ...interface{}) {
	ui.messages = append(ui.messages, fmt.Sprintf("Info: "+format, a...))
}

type testSpecCreator struct {
	t   *testing.T
	dir string
	c   Cache
}

func newTestSpecCreator(t *testing.T, ui UI) testSpecCreator {
	dir, err := ioutil.TempDir("", "spec-test")
	require.NoError(t, err)
	t.Cleanup(func() {
		e := os.RemoveAll(string(dir))
		require.NoError(t, e)
	})

	pkgCachePath := filepath.Join(dir, "PKG_CACHE")
	c := NewCache("", ui, WithPkgCachePath(pkgCachePath))

	return testSpecCreator{
		t:   t,
		dir: dir,
		c:   c,
	}
}

func (tsc testSpecCreator) create(name string, fullDirPath string, deps []SpecPackage) Spec {
	err := os.MkdirAll(fullDirPath, 0755)
	require.NoError(tsc.t, err)
	specPath := filepath.Join(fullDirPath, DefaultSpecName)
	depMap := DependencyMap{}
	prefixCounter := 0
	for _, dep := range deps {
		depMap[fmt.Sprintf("prefix%d", prefixCounter)] = dep
		prefixCounter++
	}
	spec := Spec{
		Name:        name,
		Description: "some description",
		path:        specPath,
		Deps:        depMap,
	}
	err = spec.WriteToFile()
	require.NoError(tsc.t, err)
	return spec
}

func (tsc testSpecCreator) createLocal(name string, dir string, deps []SpecPackage) Spec {
	fullDir := filepath.Join(tsc.dir, dir)
	return tsc.create(name, fullDir, deps)
}

func (tsc testSpecCreator) createUri(name string, uri string, version string, deps []SpecPackage) Spec {
	fullDir := tsc.c.PreferredPkgPath(tsc.dir, uri, version)
	return tsc.create(name, fullDir, deps)
}

func Test_Parse(t *testing.T) {
	t.Run("Parse dependencies", func(t *testing.T) {
		t.Run("Good", func(t *testing.T) {
			ui := &testUI{}
			var spec Spec
			err := spec.ParseString(`
name: foo
dependencies:
  good_url:
    url: github.com/foo/bar
  good_url_version:
    url: github.com/foo/bar
    version: ^1.0.0
  good_path:
    path: "../foobar"
  good_path_override:
    url: github.com/foo/bar
    version: ^2.0.0
    path: "../foobar"
`, ui)
			fmt.Print(ui.messages)
			require.NoError(t, err)
			assert.Len(t, ui.messages, 0)

			assert.Len(t, spec.Deps, 4)
			dep := spec.Deps["good_url"]
			assert.Equal(t, "github.com/foo/bar", dep.URL)
			dep = spec.Deps["good_path_override"]
			assert.Equal(t, "github.com/foo/bar", dep.URL)
			assert.Equal(t, "^2.0.0", dep.Version)
			assert.Equal(t, filepath.FromSlash("../foobar"), dep.Path.FilePath())
		})

		t.Run("version no url", func(t *testing.T) {
			ui := &testUI{}
			var spec Spec
			err := spec.ParseString(`
name: foo
dependencies:
  version_warning:
    version: 499
    path: foo
`, ui)
			require.NoError(t, err)
			assert.Len(t, ui.messages, 1)
			assert.Equal(t, "Warning: Prefix 'version_warning' has version constraint but no URL", ui.messages[0])
		})

		t.Run("missing all", func(t *testing.T) {
			ui := &testUI{}
			var spec Spec
			err := spec.ParseString(`
name: foo
dependencies:
  missing:
`, ui)
			assert.True(t, IsErrAlreadyReported(err))
			assert.Len(t, ui.messages, 1)
			assert.Equal(t, "Error: Package specification for prefix 'missing' is missing 'url' or 'path'", ui.messages[0])
		})

		t.Run("constraint", func(t *testing.T) {
			ui := &testUI{}
			var spec Spec
			err := spec.ParseString(`
name: foo
dependencies:
  invalid_constraint:
    url: github.com/foo/bar
    version: "not a constraint"
`, ui)
			assert.True(t, IsErrAlreadyReported(err))
			assert.Len(t, ui.messages, 1)
			assert.Equal(t, "Error: Prefix 'invalid_constraint' has invalid version constraint: 'not a constraint'", ui.messages[0])
		})

		t.Run("invalid prefix", func(t *testing.T) {
			ui := &testUI{}
			var spec Spec
			err := spec.ParseString(`
name: foo
dependencies:
  invalid-prefix:
    url: github.com/foo/bar
`, ui)
			assert.True(t, IsErrAlreadyReported(err))
			assert.Len(t, ui.messages, 1)
			assert.Equal(t, "Error: Invalid prefix: 'invalid-prefix'", ui.messages[0])
		})
	})
}

func Test_BuildSolverDeps(t *testing.T) {
	t.Run("Simple", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "sub",
			},
		})
		tsc.createLocal("sub", "sub", []SpecPackage{
			{
				URL:     "simple-url",
				Version: "1.0.0",
			},
		})
		deps, err := spec.BuildSolverDeps(&ui)
		require.NoError(t, err)
		assert.Equal(t, 1, len(deps))
		assert.Equal(t, "simple-url", deps[0].url)
	})

	t.Run("Cycle", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "pkg1",
			},
		})
		tsc.createLocal("pkg1", "pkg1", []SpecPackage{
			{
				URL:     "cycle-url1",
				Version: "1.0.0",
			},
			{
				Path: "../pkg2",
			},
		})
		tsc.createLocal("pkg2", "pkg2", []SpecPackage{
			{
				URL:     "cycle-url2",
				Version: "1.0.0",
			},
			{
				Path: "../pkg1",
			},
		})
		deps, err := spec.BuildSolverDeps(&ui)
		require.NoError(t, err)
		assert.Equal(t, 2, len(deps))
		// We find the dependency of pkg1, and of pkg2.
		assert.Equal(t, "cycle-url1", deps[0].url)
		assert.Equal(t, "cycle-url2", deps[1].url)
	})
}

func Test_VisitLocalDeps(t *testing.T) {
	t.Run("Simple", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "sub",
			},
		})
		tsc.createLocal("sub", "sub", []SpecPackage{
			{
				URL:     "simple-url",
				Version: "1.0.0",
			},
		})
		counter := 0
		err := spec.visitLocalDeps(&ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
			assert.Equal(t, filepath.Join(tsc.dir, pkgPath), fullPath)
			if counter == 0 {
				assert.Equal(t, "", pkgPath)
				assert.Len(t, depSpec.Deps, 1)
				assert.Equal(t, "sub", depSpec.Deps["prefix0"].Path.FilePath())
			} else if counter == 1 {
				assert.Equal(t, "sub", pkgPath)
				assert.Len(t, depSpec.Deps, 1)
				assert.Equal(t, "simple-url", depSpec.Deps["prefix0"].URL)
			}
			counter++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, counter)
	})

	t.Run("Abs-dotdot", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "entry", []SpecPackage{
			{
				Path: compiler.ToPath(filepath.Join("..", "dotdot")),
			},
		})
		tsc.createLocal("dotdot", "dotdot", []SpecPackage{
			{
				Path: compiler.ToPath(filepath.Join(tsc.dir, "abs")),
			},
		})
		tsc.createLocal("abs", "abs", []SpecPackage{
			{
				URL:     "abs-url",
				Version: "1.0.0",
			},
		})
		counter := 0
		err := spec.visitLocalDeps(&ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
			if counter == 0 {
				assert.Equal(t, "", pkgPath)
				p := filepath.Join(tsc.dir, "entry")
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 1 {
				assert.Equal(t, filepath.Join("..", "dotdot"), pkgPath)
				p := filepath.Join(tsc.dir, "dotdot")
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 2 {
				p := filepath.Join(tsc.dir, "abs")
				assert.Equal(t, p, pkgPath)
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			}
			counter++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 3, counter)
	})

	t.Run("Rel-dotdot", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("ab", filepath.Join("a", "b"), []SpecPackage{
			{
				Path: compiler.ToPath(filepath.Join("..", "..", "a", "c")),
			},
		})
		tsc.createLocal("ac", filepath.Join("a", "c"), []SpecPackage{
			{
				Path: compiler.ToPath(filepath.Join(tsc.dir, "abs")),
			},
		})
		tsc.createLocal("abs", "abs", []SpecPackage{
			{
				URL:     "abs-url",
				Version: "1.0.0",
			},
		})
		counter := 0
		err := spec.visitLocalDeps(&ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
			if counter == 0 {
				assert.Equal(t, "", pkgPath)
				p := filepath.Join(tsc.dir, "a", "b")
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 1 {
				assert.Equal(t, filepath.Join("..", "c"), pkgPath)
				p := filepath.Join(tsc.dir, "a", "c")
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 2 {
				p := filepath.Join(tsc.dir, "abs")
				assert.Equal(t, p, pkgPath)
				assert.Equal(t, p, fullPath)
				assert.Len(t, depSpec.Deps, 1)
			}
			counter++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 3, counter)
	})

	t.Run("Cycle", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "pkg1",
			},
		})
		tsc.createLocal("pkg1", "pkg1", []SpecPackage{
			{
				URL:     "cycle-url1",
				Version: "1.0.0",
			},
			{
				Path: "../pkg2",
			},
		})
		tsc.createLocal("pkg2", "pkg2", []SpecPackage{
			{
				URL:     "cycle-url2",
				Version: "1.0.0",
			},
			{
				Path: "..",
			},
		})
		counter := 0
		err := spec.visitLocalDeps(&ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
			assert.Equal(t, filepath.Join(tsc.dir, pkgPath), fullPath)
			if counter == 0 {
				assert.Equal(t, "", pkgPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 1 {
				assert.Equal(t, "pkg1", pkgPath)
				assert.Len(t, depSpec.Deps, 2)
				assert.Equal(t, "cycle-url1", depSpec.Deps["prefix0"].URL)
			} else if counter == 2 {
				assert.Equal(t, "pkg2", pkgPath)
				assert.Len(t, depSpec.Deps, 2)
				assert.Equal(t, "cycle-url2", depSpec.Deps["prefix0"].URL)
			}
			counter++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 3, counter)
	})

	t.Run("Long chain", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "pkg1",
			},
		})
		tsc.createLocal("pkg1", "pkg1", []SpecPackage{
			{
				Path: "../pkg2",
			},
		})
		tsc.createLocal("pkg2", "pkg2", []SpecPackage{
			{
				Path: "../pkg3",
			},
		})
		tsc.createLocal("pkg3", "pkg3", []SpecPackage{
			{
				Path: "../pkg4",
			},
		})
		tsc.createLocal("pkg4", "pkg4", []SpecPackage{
			{
				URL:     "url4",
				Version: "1.0.0",
			},
		})
		counter := 0
		err := spec.visitLocalDeps(&ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
			assert.Equal(t, filepath.Join(tsc.dir, pkgPath), fullPath)
			if counter == 0 {
				assert.Equal(t, "", pkgPath)
			} else if counter < 4 {
				assert.Equal(t, fmt.Sprintf("pkg%d", counter), pkgPath)
				assert.Len(t, depSpec.Deps, 1)
			} else if counter == 3 {
				assert.Equal(t, "pkg4", pkgPath)
				assert.Len(t, depSpec.Deps, 1)
				assert.Equal(t, "url4", depSpec.Deps["prefix0"].URL)
			}
			counter++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 5, counter)
	})
}

func makeStringVersions(t *testing.T, vStrs ...string) []StringVersion {
	result := []StringVersion{}
	for _, vStr := range vStrs {
		v, err := version.NewVersion(vStr)
		require.NoError(t, err)
		result = append(result, StringVersion{
			vStr: vStr,
			v:    v,
		})
	}
	return result
}

func Test_SpecToLock(t *testing.T) {
	t.Run("Local", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				Path: "local_path",
			},
			{
				Path: "local_path2",
			},
		})
		solution := &Solution{}
		lf, err := spec.BuildLockFile(solution, tsc.c, Registries{}, &ui)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tsc.dir, DefaultLockFileName), lf.path)
		assert.Equal(t, 2, len(lf.Prefixes))
		assert.Equal(t, 2, len(lf.Packages))
		pkgID, ok := lf.Prefixes["prefix0"]
		require.True(t, ok)
		pkgEntry, ok := lf.Packages[pkgID]
		require.True(t, ok)
		assert.Equal(t, "local_path", pkgEntry.Path.FilePath())
		assert.Equal(t, 0, len(pkgEntry.Prefixes))

		pkgID, ok = lf.Prefixes["prefix1"]
		require.True(t, ok)
		pkgEntry, ok = lf.Packages[pkgID]
		require.True(t, ok)
		assert.Equal(t, "local_path2", pkgEntry.Path.FilePath())
		assert.Equal(t, 0, len(pkgEntry.Prefixes))
	})
	t.Run("Constraints", func(t *testing.T) {
		ui := testUI{}
		tsc := newTestSpecCreator(t, &ui)
		spec := tsc.createLocal("", "", []SpecPackage{
			{
				URL:     "simple-url",
				Version: "1.0.0",
			},
			{
				URL:     "simple-url2",
				Version: ">=1.0.0,<2.0.0",
			},
			{
				URL:     "simple-url2",
				Version: ">=2.0.0,<3.0.0",
			},
		})
		tsc.createUri("simple_url", "simple-url", "1.0.0", []SpecPackage{
			{
				URL:     "simple-url2",
				Version: ">=2.1.0,<2.5.0",
			},
		})
		tsc.createUri("simple_url2", "simple-url2", "1.2.5", []SpecPackage{})
		tsc.createUri("simple_url2", "simple-url2", "2.3.4", []SpecPackage{})
		solution := &Solution{
			pkgs: map[string][]StringVersion{
				"simple-url":  makeStringVersions(t, "1.0.0"),
				"simple-url2": makeStringVersions(t, "1.2.5", "2.3.4"),
			},
		}
		lf, err := spec.BuildLockFile(solution, tsc.c, Registries{}, &ui)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tsc.dir, DefaultLockFileName), lf.path)
		assert.Equal(t, 3, len(lf.Prefixes))
		assert.Equal(t, 3, len(lf.Packages))
		pkgID, ok := lf.Prefixes["prefix0"]
		require.True(t, ok)
		pkgEntry, ok := lf.Packages[pkgID]
		require.True(t, ok)
		assert.Equal(t, "", pkgEntry.Path.FilePath())
		assert.Equal(t, "simple-url", pkgEntry.URL.URL())
		assert.Equal(t, "1.0.0", pkgEntry.Version)
		assert.Equal(t, 1, len(pkgEntry.Prefixes))
		pkgID, ok = pkgEntry.Prefixes["prefix0"]
		require.True(t, ok)
		otherID, ok := lf.Prefixes["prefix2"]
		require.True(t, ok)
		// We will check later that the prefix0 and prefix2 go towards simple-url2/2.3.4.
		assert.Equal(t, pkgID, otherID)

		pkgID, ok = lf.Prefixes["prefix1"]
		require.True(t, ok)
		pkgEntry, ok = lf.Packages[pkgID]
		require.True(t, ok)
		assert.Equal(t, "", pkgEntry.Path.FilePath())
		assert.Equal(t, "simple-url2", pkgEntry.URL.URL())
		assert.Equal(t, "1.2.5", pkgEntry.Version)
		assert.Equal(t, 0, len(pkgEntry.Prefixes))

		pkgID, ok = lf.Prefixes["prefix2"]
		require.True(t, ok)
		pkgEntry, ok = lf.Packages[pkgID]
		require.True(t, ok)
		assert.Equal(t, "", pkgEntry.Path.FilePath())
		assert.Equal(t, "simple-url2", pkgEntry.URL.URL())
		assert.Equal(t, "2.3.4", pkgEntry.Version)
		assert.Equal(t, 0, len(pkgEntry.Prefixes))
	})
}
