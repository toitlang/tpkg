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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/toitlang/tpkg/pkg/compiler"
)

func Test_OptimizePkgIDs(t *testing.T) {
	t.Run("Simple", func(t *testing.T) {
		lc := LockFile{}
		projectURL := "github.com/company/project"
		projectVersion := "1.0.0"
		project2URL := "github.com/company/project2"
		project2Version := "1.2.3"
		project3URL := "github.com/company/project3"
		project3Version := "0.0.1-beta"
		otherPath := "other"

		lc.Packages = map[string]PackageEntry{
			"pkg1": {
				URL:     compiler.ToURIPath(projectURL),
				Version: projectVersion,
				Prefixes: PrefixMap{
					"pre1": "pkg1",
					"pre2": "pkg2",
					"pre3": "pkg3",
					"pre4": "other",
				},
			},
			"pkg2": {
				URL:     compiler.ToURIPath(project2URL),
				Name:    "project2",
				Version: project2Version,
			},
			"pkg3": {
				URL:     compiler.ToURIPath(project3URL),
				Name:    "project3",
				Version: project3Version,
			},
			"other": {
				Name: "other",
				Path: compiler.ToPath(otherPath),
			},
		}
		lc.Prefixes = PrefixMap{
			"pre1": "pkg1",
			"pre2": "pkg2",
			"pre3": "pkg3",
			"pre4": "other",
		}
		lc.optimizePkgIDs()

		checkPrefixes := func(prefixes PrefixMap) {
			assert.Equal(t, 4, len(prefixes))
			pre1, ok := prefixes["pre1"]
			assert.True(t, ok)
			assert.Equal(t, "project", pre1)
			pre2, ok := prefixes["pre2"]
			assert.True(t, ok)
			assert.Equal(t, "project2", pre2)
			pre3, ok := prefixes["pre3"]
			assert.True(t, ok)
			assert.Equal(t, "project3", pre3)
			pre4, ok := prefixes["pre4"]
			assert.True(t, ok)
			assert.Equal(t, "other", pre4)
		}

		assert.Equal(t, 4, len(lc.Packages))
		entry, ok := lc.Packages["project"]
		checkPrefixes(entry.Prefixes)
		assert.True(t, ok)
		assert.Equal(t, "", entry.Path.FilePath())
		assert.Equal(t, projectURL, entry.URL.URL())
		assert.Equal(t, projectVersion, entry.Version)

		entry, ok = lc.Packages["project2"]
		assert.True(t, ok)
		assert.Nil(t, entry.Prefixes)
		assert.Equal(t, "", entry.Path.FilePath())
		assert.Equal(t, project2URL, entry.URL.URL())
		assert.Equal(t, project2Version, entry.Version)

		entry, ok = lc.Packages["project3"]
		assert.True(t, ok)
		assert.Nil(t, entry.Prefixes)
		assert.Equal(t, "", entry.Path.FilePath())
		assert.Equal(t, project3URL, entry.URL.URL())
		assert.Equal(t, project3Version, entry.Version)

		entry, ok = lc.Packages["other"]
		assert.True(t, ok)
		assert.Nil(t, entry.Prefixes)
		assert.Equal(t, otherPath, entry.Path.FilePath())

		checkPrefixes(lc.Prefixes)
	})

	t.Run("Hard", func(t *testing.T) {
		lc := LockFile{}
		lc.Packages = map[string]PackageEntry{}
		lc.Prefixes = PrefixMap{}

		tests := map[string]string{
			// Just the project-name.
			"github.com/company/project0-1.0.0": "project0",

			// Need version.
			"github.com/company/project1-1.0.0": "project1-1.0.0",
			"github.com/company/project1-2.3.4": "project1-2.3.4",

			// Need company.
			"github.com/company/project2-1.0.0":  "company/project2",
			"github.com/company2/project2-2.3.4": "company2/project2",

			// Need company + version.
			"github.com/company/project3-1.0.0":  "company/project3-1.0.0",
			"github.com/company/project3-2.3.4":  "company/project3-2.3.4",
			"github.com/company2/project3-1.0.0": "company2/project3",

			// Need url + company + version.
			"github.com/company/project4-1.0.0": "github.com/company/project4",
			"gitlab.com/company/project4-1.0.0": "gitlab.com/company/project4",

			// One URL has more segments than another.
			// Should never happen in practice, but we know what to expect if it does.
			"github.com/company/project5-1.0.0":           "github.com/company/project5",
			"something/github.com/company/project5-1.0.0": "something/github.com/company/project5",
		}

		i := 0
		for urlVersion := range tests {
			parts := strings.Split(urlVersion, "-")
			url := parts[0]
			version := parts[1]
			lc.Packages[fmt.Sprintf("pkg%d", i)] = PackageEntry{
				URL:     compiler.ToURIPath(url),
				Version: version,
			}
			i++
		}

		lc.optimizePkgIDs()

		assert.Equal(t, len(tests), len(lc.Packages))

		for urlVersion, expectedID := range tests {
			parts := strings.Split(urlVersion, "-")
			url := parts[0]
			version := parts[1]
			entry, ok := lc.Packages[expectedID]
			assert.True(t, ok)
			assert.Equal(t, "", entry.Path.FilePath())
			assert.Equal(t, url, entry.URL.URL())
			assert.Equal(t, version, entry.Version)
		}
	})

	t.Run("Escaped_Ambiguous", func(t *testing.T) {
		lc := LockFile{}
		lc.Packages = map[string]PackageEntry{}
		lc.Prefixes = PrefixMap{}

		paths := map[string]bool{
			// Due to the escaping the following prefixes would end up the same.
			"github.com/company/project##-1.0.0": true,
			"github.com/company/project%%-1.0.0": true,
			// Innocent bystander (the ID is valid).
			"github.com/company/project__-1.0.0": true,

			// The 1.0.0 versions will be ambiguous.
			// The 1.1.2 and 2.0.0 won't.
			"github.com/company/project2##-1.0.0": true,
			"github.com/company/project2##-1.1.2": true,
			"github.com/company/project2%%-1.0.0": true,
			"github.com/company/project2%%-2.0.0": true,
		}
		i := 0
		for urlVersion := range paths {
			parts := strings.Split(urlVersion, "-")
			url := parts[0]
			version := parts[1]
			lc.Packages[fmt.Sprintf("pkg%d", i)] = PackageEntry{
				URL:     compiler.ToURIPath(url),
				Version: version,
			}
			i++
		}

		lc.optimizePkgIDs()

		assert.Equal(t, len(lc.Packages), len(paths))

		alreadySeen := map[string]bool{}

		expectedIDs := map[string]bool{
			"github.com/company/project__--0":        true,
			"github.com/company/project__--1":        true,
			"github.com/company/project__--2":        true,
			"github.com/company/project2__-1.1.2":    true,
			"github.com/company/project2__-2.0.0":    true,
			"github.com/company/project2__-1.0.0--0": true,
			"github.com/company/project2__-1.0.0--1": true,
		}
		for actualID, entry := range lc.Packages {
			assert.Contains(t, expectedIDs, actualID)
			urlVersion := fmt.Sprintf("%s-%s", entry.URL.URL(), entry.Version)
			assert.Contains(t, paths, urlVersion)
			assert.NotContains(t, alreadySeen, actualID)
			alreadySeen[actualID] = true
			assert.NotContains(t, alreadySeen, urlVersion)
			alreadySeen[urlVersion] = true
		}
	})
}
