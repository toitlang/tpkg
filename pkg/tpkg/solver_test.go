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

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func containsVersion(versions []StringVersion, vStr string) bool {
	for _, strVersion := range versions {
		if strVersion.vStr == vStr {
			return true
		}
	}
	return false
}

func mkPkg(nameVersion string, depStrs ...string) *Desc {
	parts := strings.Split(nameVersion, "-")
	name := parts[0]
	version := parts[1]
	deps := []descPackage{}
	for _, dep := range depStrs {
		index := strings.Index(dep, " ")
		deps = append(deps, descPackage{
			URL:     dep[:index],
			Version: dep[index+1:],
		})
	}
	return NewDesc(name, "", name, version, "", "MIT", "", deps)
}

func makeRegistries(testPkgs ...*Desc) Registries {
	pr := pathRegistry{
		path:    "not important",
		entries: testPkgs,
	}
	return Registries{
		&pr,
	}
}

func checkSolution(t *testing.T, solution *Solution, descs ...*Desc) {
	require.NotNil(t, solution)
	perURL := map[string][]*Desc{}
	for _, desc := range descs {
		perURL[desc.URL] = append(perURL[desc.URL], desc)
	}
	for _, desc := range descs {
		versions, ok := solution.pkgs[desc.URL]
		require.True(t, ok)
		assert.Len(t, versions, len(perURL[desc.URL]))
		assert.True(t, containsVersion(versions, desc.Version))
	}
	count := 0
	for _, versions := range solution.pkgs {
		count = count + len(versions)
	}
	assert.Equal(t, count, len(descs))
}

func preferred(descs ...*Desc) []versionedURL {
	result := []versionedURL{}
	for _, pref := range descs {
		result = append(result, versionedURL{
			URL:     pref.URL,
			Version: pref.Version,
		})
	}
	return result
}

func findSolutionSDKUI(t *testing.T, solveFor *Desc, registries Registries, sdkVersion *version.Version, preferred ...[]versionedURL) (*Solution, *testUI) {
	ui := testUI{}
	solver, err := NewSolver(registries, sdkVersion, &ui)
	require.NoError(t, err)
	if len(preferred) != 0 {
		for _, pref := range preferred {
			solver.SetPreferred(pref)
		}
	}
	startConstraint, err := parseConstraint(solveFor.Version)
	require.NoError(t, err)

	solveForSDK, err := sdkConstraintToMinSDK(solveFor.Environment.SDK)
	require.NoError(t, err)

	solution := solver.Solve(solveForSDK, []SolverDep{
		{
			url:         solveFor.URL,
			constraints: startConstraint,
		},
	})
	return solution, &ui
}

func findSolutionUI(t *testing.T, solveFor *Desc, registries Registries, preferred ...[]versionedURL) (*Solution, *testUI) {
	return findSolutionSDKUI(t, solveFor, registries, nil, preferred...)
}

func findSolution(t *testing.T, solveFor *Desc, registries Registries, preferred ...[]versionedURL) *Solution {
	return findSolutionSDK(t, solveFor, registries, nil, preferred...)
}

func findSolutionSDK(t *testing.T, solveFor *Desc, registries Registries, sdkVersion *version.Version, preferred ...[]versionedURL) *Solution {
	result, ui := findSolutionSDKUI(t, solveFor, registries, sdkVersion, preferred...)
	for _, msg := range ui.messages {
		fmt.Println(msg)
	}
	assert.Empty(t, ui.messages)
	return result
}

func Test_Solver(t *testing.T) {
	t.Run("Solve Transitive", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		b11 := mkPkg("b-1.1.0", "c >=2.0.0,<3.1.2")
		c2 := mkPkg("c-2.0.5")
		registries := makeRegistries(a1, b11, c2)
		solution := findSolution(t, a1, registries)
		checkSolution(t, solution, a1, b11, c2)
	})

	t.Run("Solve Correct Version", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		b01 := mkPkg("b-0.1.0")
		b11 := mkPkg("b-1.1.0")
		b21 := mkPkg("b-2.1.0")
		registries := makeRegistries(a1, b01, b11, b21)
		solution := findSolution(t, a1, registries)
		checkSolution(t, solution, a1, b11)
	})

	t.Run("Solve Highest Version", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		b111 := mkPkg("b-1.1.1")
		b123 := mkPkg("b-1.2.3")
		b21 := mkPkg("b-2.1.0")
		registries := makeRegistries(a1, b111, b123, b21)
		solution := findSolution(t, a1, registries)
		checkSolution(t, solution, a1, b123)
	})

	t.Run("Solve Multiple Version", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0", "c ^1.0.0")
		b111 := mkPkg("b-1.1.1", "c ^2.0.0")
		c1 := mkPkg("c-1.2.3")
		c2 := mkPkg("c-2.3.4")
		registries := makeRegistries(a1, b111, c1, c2)
		solution := findSolution(t, a1, registries)
		checkSolution(t, solution, a1, b111, c1, c2)
	})

	t.Run("Solve Cycle", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		b111 := mkPkg("b-1.1.1", "a ^1.0.0")
		registries := makeRegistries(a1, b111)
		solution := findSolution(t, a1, registries)
		checkSolution(t, solution, a1, b111)
	})

	t.Run("Fail Missing Pkg", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		registries := makeRegistries(a1)
		solution, ui := findSolutionUI(t, a1, registries)
		assert.Nil(t, solution)
		assert.Len(t, ui.messages, 1)
		assert.Equal(t, "Warning: Package 'b' not found", ui.messages[0])
	})

	t.Run("Fail Version", func(t *testing.T) {
		a1 := mkPkg("a-1.7.0", "b ^1.0.0")
		b234 := mkPkg("b-2.3.4")
		registries := makeRegistries(a1, b234)
		solution, ui := findSolutionUI(t, a1, registries)
		assert.Nil(t, solution)
		assert.Len(t, ui.messages, 1)
		assert.Equal(t, "Warning: No version of 'b' satisfies constraint '>=1.0.0,<2.0.0'", ui.messages[0])
	})

	t.Run("Preferred", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^1.0.0")
		b110 := mkPkg("b-1.1.0")
		b111 := mkPkg("b-1.1.1")
		b210 := mkPkg("b-2.1.0")
		registries := makeRegistries(a170, b110, b111, b210)
		// Prefer "b110".
		solution := findSolution(t, a170, registries, preferred(b110))
		checkSolution(t, solution, a170, b110)
	})

	t.Run("Solve Backtrack", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^1.0.0", "c ^1.0.0")
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0")
		c100 := mkPkg("c-1.0.0", "b >=1.0.0,<1.5.0")
		// The dependency 'b ^1.0.0' will first find b180 which doesn't work for c. It
		// will backtrack, then try b140 which is then successful for c.
		registries := makeRegistries(a170, b140, b180, c100)
		solution := findSolution(t, a170, registries)
		checkSolution(t, solution, a170, b140, c100)
	})

	t.Run("Solve No Backtrack Preferred", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^1.0.0", "c ^1.0.0")
		b130 := mkPkg("b-1.3.0")
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0")
		c100 := mkPkg("c-1.0.0", "b >=1.0.0,<1.5.0")
		registries := makeRegistries(a170, b130, b140, b180, c100)
		// With the preferred "b140", no backtracking is needed.
		solution := findSolution(t, a170, registries, preferred(b130))
		checkSolution(t, solution, a170, b130, c100)
	})

	t.Run("Solve 2 versions", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^2.0.0", "c ^1.0.0")
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0")
		b200 := mkPkg("b-2.0.0")
		c100 := mkPkg("c-1.0.0", "b >=1.0.0,<1.5.0")
		registries := makeRegistries(a170, b140, b180, b200, c100)
		solution := findSolution(t, a170, registries)
		checkSolution(t, solution, a170, b140, b200, c100)
	})

	t.Run("Solve Backtrack 2 versions", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^2.0.0", "c ^1.0.0", "d <1.5.0")
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0")
		b200 := mkPkg("b-2.0.0")
		c100 := mkPkg("c-1.0.0", "b >=1.0.0,<1.5.0")
		// c150 will conflict with the d-version of a170.
		// This will lead to c100 being selected, which will then require
		// another major version of 'd' (namely b140).
		c150 := mkPkg("c-1.0.0", "b ^2.0.0", "d ^1.5.8")
		d140 := mkPkg("d-1.4.0")
		d160 := mkPkg("d-1.6.0")
		registries := makeRegistries(a170, b140, b180, b200, c100, c150, d140, d160)
		solution := findSolution(t, a170, registries)
		checkSolution(t, solution, a170, b140, b200, c100, d140)
	})

	t.Run("Uniq error message", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b >=1.0.0", "c >=1.0.0")
		// The solver will try b200, then b180, each time needing to backtrack because
		// of the bad d-dependency which can't be satisfied.
		// It will re-evaluate the 'c' dependency each time, which has warnings.
		// Similarly, it will encounter the d200 at least twice.
		// These warnings must not be printed multiple times.
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0", "d >=1.3.0")
		b200 := mkPkg("b-2.0.0", "d >=1.3.0")
		c100 := mkPkg("c-1.0.0")
		c150 := mkPkg("c-1.5.0", "b >=3.0.0") // No b-package satisfies this requirement.
		d123 := mkPkg("d-1.2.3")
		d200 := mkPkg("d-1.5.0", "e >=3.0.0") // No e-package exists.
		registries := makeRegistries(a170, b140, b180, b200, c100, c150, d123, d200)
		solution, ui := findSolutionUI(t, a170, registries)
		checkSolution(t, solution, a170, b140, c100)
		assert.Len(t, ui.messages, 2)
		assert.Equal(t, "Warning: No version of 'b' satisfies constraint '>=3.0.0'", ui.messages[0])
		assert.Equal(t, "Warning: Package 'e' not found", ui.messages[1])
	})

	t.Run("MinSDK", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^2.0.0", "c ^1.0.0")
		b140 := mkPkg("b-1.4.0")
		b180 := mkPkg("b-1.8.0")
		b200 := mkPkg("b-2.0.0")
		c100 := mkPkg("c-1.0.0", "b >=1.0.0,<1.5.0")
		v110, err := version.NewVersion("1.1.0")
		require.NoError(t, err)
		v120, err := version.NewVersion("1.2.0")
		require.NoError(t, err)
		v130, err := version.NewVersion("1.3.0")
		require.NoError(t, err)
		b140.Environment.SDK = "^" + v110.String()
		b180.Environment.SDK = "^" + v130.String()
		b200.Environment.SDK = "^" + v120.String()
		registries := makeRegistries(a170, b140, b180, b200, c100)
		solution := findSolution(t, a170, registries)
		checkSolution(t, solution, a170, b140, b200, c100)
		assert.Equal(t, v120.String(), solution.minSDK.String())
	})

	t.Run("SDKVersion", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^1.0.0")
		b140 := mkPkg("b-1.4.0")
		b160 := mkPkg("b-1.6.0")
		b180 := mkPkg("b-1.8.0")
		v110, err := version.NewVersion("1.1.0")
		require.NoError(t, err)
		v115, err := version.NewVersion("1.1.5")
		require.NoError(t, err)
		v120, err := version.NewVersion("1.2.0")
		require.NoError(t, err)
		v130, err := version.NewVersion("1.3.0")
		require.NoError(t, err)
		b140.Environment.SDK = "^" + v110.String()
		b160.Environment.SDK = "^" + v120.String()
		b180.Environment.SDK = "^" + v130.String()
		registries := makeRegistries(a170, b140, b160, b180)
		solution := findSolution(t, a170, registries)
		checkSolution(t, solution, a170, b180)
		assert.Equal(t, v130.String(), solution.minSDK.String())

		solution = findSolutionSDK(t, a170, registries, v115)
		checkSolution(t, solution, a170, b140)
		assert.Equal(t, v110.String(), solution.minSDK.String())
	})

	t.Run("SDKVersion Fail", func(t *testing.T) {
		a170 := mkPkg("a-1.7.0", "b ^1.0.0")
		b140 := mkPkg("b-1.4.0")
		b160 := mkPkg("b-1.6.0")
		b180 := mkPkg("b-1.8.0")
		v105, err := version.NewVersion("1.0.5")
		require.NoError(t, err)
		v110, err := version.NewVersion("1.1.0")
		require.NoError(t, err)
		v120, err := version.NewVersion("1.2.0")
		require.NoError(t, err)
		v130, err := version.NewVersion("1.3.0")
		require.NoError(t, err)
		b140.Environment.SDK = "^" + v110.String()
		b160.Environment.SDK = "^" + v120.String()
		b180.Environment.SDK = "^" + v130.String()
		registries := makeRegistries(a170, b140, b160, b180)

		solution, ui := findSolutionSDKUI(t, a170, registries, v105)
		assert.Nil(t, solution)
		assert.Len(t, ui.messages, 1)
		assert.Equal(t, "Warning: No version of 'b' satisfies constraint '>=1.0.0,<2.0.0' with SDK version 1.0.5", ui.messages[0])

		a170.Environment.SDK = "^" + v110.String()
		solution, ui = findSolutionSDKUI(t, a170, registries, v105)
		assert.Nil(t, solution)
		assert.Len(t, ui.messages, 1)
		assert.Equal(t, "Warning: SDK version '1.0.5' does not satisfy the minimal SDK requirement '^1.1.0'", ui.messages[0])
	})
}
