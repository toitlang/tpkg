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
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/toitlang/tpkg/pkg/set"
)

// Solver is a simple constraint solver for the Toit package manager.
type Solver struct {
	db            pkgDB
	ui            UI
	state         solverState
	printedErrors set.String
	// sdkVersion is the SDK version the application runs on.
	// All packages must satisfy this version.
	sdkVersion *version.Version
}

// pkgDB is a map from package-url to all the existing packages of that url.
// The solver tries the list of packages one by one to find the one that
// works. As such, the order of packages is important.
type pkgDB map[string][]solverPkg

// solverPkg represents a package in the solver.
// It only needs the version and the dependencies. (The name is stored as
// key in the map that contains all packages).
type solverPkg struct {
	version *version.Version
	deps    []SolverDep
	minSDK  *version.Version
}

// SolverDep represents a dependency for the solver.
// It needs the target's package name and the version constraints for it.
type SolverDep struct {
	url         string
	constraints version.Constraints
}

// Solution is a map from pkg-url to a set of version-strings.
type Solution struct {
	pkgs   map[string][]StringVersion
	minSDK *version.Version
}

type StringVersion struct {
	vStr string
	v    *version.Version
}

type solverState struct {
	// The partial solution so far.
	// Goes from url-major to the precise version.
	pkgs map[string]*version.Version

	// The minimal SDK version that is required for the partial solution.
	// May be nil if there isn't any constraint.
	minSDK *version.Version

	// The dependencies we are trying to satisfy.
	// Dependencies on the same package may appear multiple times. In that case
	// the entry will take into account which version was chosen earlier.
	workingQueue []*SolverDep

	// continuations contains the information necessary to continue
	// exploring all possible packages for a dependency.
	continuations continuationsStack

	// Undo information if a candidate didn't work.
	// We need to undo the modifications we made before we try the next entry in
	// the list of possible packages.
	undos undoStack
}

type continuationsStack []solverContinuation
type undoStack []undoInfo

func (cs *continuationsStack) Push(cont solverContinuation) {
	*cs = append(*cs, cont)
}

func (cs *continuationsStack) Pop() solverContinuation {
	l := len(*cs)
	result := (*cs)[l-1]
	*cs = (*cs)[:l-1]
	return result
}

func (us *undoStack) Push(undo undoInfo) {
	*us = append(*us, undo)
}

func (us *undoStack) Pop() undoInfo {
	l := len(*us)
	result := (*us)[l-1]
	*us = (*us)[:l-1]
	return result
}

// The index into the solverPkg slice as given by the pkgDB.
// The solver will go through all possible entries and see if one works.
// If an earlier workingEntry already added a concrete version to the
// partial solution, then the solver will only try major versions for this
// entry.
type solverContinuation struct {
	index int
}

type undoInfo struct {
	// The length of the working queue at the time we encounter the new entry.
	// We have to trim all entries we added.
	workingQueueLen int
	// The urlVersion we have to remove. Empty if there was already one.
	urlVersion string
	// The minimal SDK version that is required for the partial solution.
	minSDK *version.Version
}

// versionedURL combines a URL and a version.
type versionedURL struct {
	URL     string
	Version string
}

type byVersion []solverPkg

func (a byVersion) Len() int           { return len(a) }
func (a byVersion) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byVersion) Less(i, j int) bool { return a[i].version.LessThan(a[j].version) }

func NewSolverDep(url string, constraintString string) (SolverDep, error) {
	constraints := version.Constraints{}
	if constraintString != "" {
		var err error
		constraints, err = parseConstraint(constraintString)
		if err != nil {
			return SolverDep{}, err
		}
	}

	return SolverDep{
		url:         url,
		constraints: constraints,
	}, nil
}

func convertDeps(descDeps []descPackage) ([]SolverDep, error) {
	result := []SolverDep{}
	for _, dep := range descDeps {
		solverDep, err := NewSolverDep(dep.URL, dep.Version)
		if err != nil {
			return nil, err
		}
		result = append(result, solverDep)
	}
	return result, nil
}

func NewSolver(registries Registries, sdkVersion *version.Version, ui UI) (*Solver, error) {
	result := &Solver{
		db:         map[string][]solverPkg{},
		ui:         ui,
		sdkVersion: sdkVersion,
	}

	for _, reg := range registries {
		for _, desc := range reg.Entries() {
			v, err := version.NewVersion(desc.Version)
			if err != nil {
				return nil, err
			}
			deps, err := convertDeps(desc.Deps)
			if err != nil {
				return nil, err
			}
			minSDK, err := sdkConstraintToMinSDK(desc.Environment.SDK)
			if err != nil {
				return nil, err
			}
			pkgs := result.db[desc.URL]
			pkgs = append(pkgs, solverPkg{
				version: v,
				deps:    deps,
				minSDK:  minSDK,
			})
			result.db[desc.URL] = pkgs
		}
	}
	// Sort entries.
	for _, pkgs := range result.db {
		sort.Sort(byVersion(pkgs))
		// Reverse the list, as we want higher versions first.
		// We could try to sort the pkgs already in opposite version order, but that
		// would require writing a different `byVersion` comparator, or wrapping it.
		// It's just easier and cleaner to reverse the list after having sorted it.
		for i, j := 0, len(pkgs)-1; i < j; i, j = i+1, j-1 {
			pkgs[i], pkgs[j] = pkgs[j], pkgs[i]
		}
	}
	return result, nil
}

// SetPreferred marks the list of versionedURLs as preferred.
func (s *Solver) SetPreferred(preferred []versionedURL) {
	// Start from the back, so that the given preferred versions are found in order.
	for i := len(preferred) - 1; i >= 0; i-- {
		versioned := preferred[i]
		url := versioned.URL
		version, err := version.NewVersion(versioned.Version)
		if err != nil {
			// Version didn't parse. Just skip it.
			continue
		}
		pkgs, ok := s.db[url]
		if !ok {
			// We don't have a package with that URL.
			continue
		}
		// In theory we could try to use binary search (since the packages are sorted by
		// version). However, we don't have a guarantee that we didn't already reorder the
		// packages with a preferred package.
		for j := 0; j < len(pkgs); j++ {
			pkg := pkgs[j]
			if pkg.version.Equal(version) {
				// Take the current pkg and move it to the first slot.
				for k := j; k > 0; k-- {
					pkgs[k] = pkgs[k-1]
				}
				pkgs[0] = pkg
				break
			}
		}
		// We might not have found the version, but that's fine. In that case we
		// didn't modify anything.
	}
}

func (s *Solver) solveDep(dep *SolverDep, cont solverContinuation) (bool, solverContinuation, undoInfo) {
	url := dep.url
	available, ok := s.db[url]

	if !ok {
		msg := fmt.Sprintf("Package '%s' not found", url)
		if !s.printedErrors.Contains(msg) {
			s.ui.ReportWarning(msg)
			s.printedErrors.Add(msg)
		}
		return false, solverContinuation{}, undoInfo{}
	}

	index := cont.index
	constraints := dep.constraints
	foundSatisfying := index != 0 // We already found one last time.
	sdkMismatch := false
	// Annoyingly we still need to run through all available packages,
	// even if an earlier entry already fixed a version. This is, because
	// the dependency might allow multiple major versions, and we only
	// use earlier selections if they have the same major version.
	for index < len(available) {
		candidate := available[index]
		index++
		if !constraints.Check(candidate.version) {
			continue
		}
		if s.sdkVersion != nil && candidate.minSDK != nil && s.sdkVersion.LessThan(candidate.minSDK) {
			sdkMismatch = true
			continue
		}
		foundSatisfying = true
		major := candidate.version.Segments()[0]
		urlVersion := url + "-" + fmt.Sprint(major)
		existing, ok := s.state.pkgs[urlVersion]
		if ok {
			if candidate.version != existing {
				// We only look at the same version as defined by an earlier dependency.
				continue
			}
		}

		undo := undoInfo{
			minSDK: s.state.minSDK,
			// Keep track of which dependencies we add for this dependency.
			workingQueueLen: len(s.state.workingQueue),
		}
		if !ok {
			// First time we set a concrete version for this URL-major.
			if s.state.minSDK == nil ||
				(candidate.minSDK != nil && candidate.minSDK.GreaterThan(s.state.minSDK)) {
				s.state.minSDK = candidate.minSDK
			}
			s.state.pkgs[urlVersion] = candidate.version
			s.addDeps(candidate.deps)
			// If we undo this entry, we have to remove it from the partial solution.
			undo.urlVersion = urlVersion
		}
		return true, solverContinuation{index: index}, undo
	}
	if !foundSatisfying {
		msg := ""
		if constraints.String() != "" {
			msg = fmt.Sprintf("No version of '%s' satisfies constraint '%s'", url, constraints.String())
			if sdkMismatch {
				msg += " with SDK version " + s.sdkVersion.String()
			}
		} else {
			msg = fmt.Sprintf("No version of '%s' exists for SDK version '%s'", url, s.sdkVersion.String())
		}
		if !s.printedErrors.Contains(msg) {
			s.ui.ReportWarning(msg)
			s.printedErrors.Add(msg)
		}
	}

	// Return a failure.
	return false, solverContinuation{}, undoInfo{}
}

// addDeps adds all dependencies to the working queue.
// They will be checked when it's their turn.
func (s *Solver) addDeps(deps []SolverDep) {
	for _, dep := range deps {
		localDep := dep
		s.state.workingQueue = append(s.state.workingQueue, &localDep)
	}
}

func (s *Solver) applyUndo(undo undoInfo) {
	if undo.workingQueueLen != 0 {
		s.state.workingQueue = s.state.workingQueue[:undo.workingQueueLen]
	}
	if undo.urlVersion != "" {
		delete(s.state.pkgs, undo.urlVersion)
	}
	s.state.minSDK = undo.minSDK
}

func (s *Solver) Solve(minSDK *version.Version, deps []SolverDep) *Solution {
	if s.sdkVersion != nil && minSDK != nil {
		if s.sdkVersion.LessThan(minSDK) {
			s.ui.ReportWarning("SDK version '%s' does not satisfy the minimal SDK requirement '^%s'",
				s.sdkVersion.String(), minSDK.String())
			return nil
		}
	}
	s.state = solverState{
		pkgs:          map[string]*version.Version{},
		minSDK:        minSDK,
		workingQueue:  []*SolverDep{},
		undos:         []undoInfo{},
		continuations: []solverContinuation{},
	}
	s.addDeps(deps)
	workingIndex := 0
	// Solving strategy:
	// - The working queue contains dependencies that haven't been solved yet.
	//   There might already be a concrete version for them in the partial solution
	//   but we haven't checked that yet.
	// - For each entry we try all possible solutions, taking earlier selection into
	//   account. Note that some dependencies might allow multiple major versions, in
	//   which case an earlier entry with the same dependency URL might not be used.
	// - We try to find a working solution at each entry and then proceed to the next
	//   one. (Before that we add the new dependencies).
	// - The continuations queue contains the information necessary to test the next
	//   package if we don't find a solution with the current one.
	// - The undo-queue contains the backtracking information.
	for {
		if workingIndex >= len(s.state.workingQueue) {
			// We have successfully handled all workingQueue entries.
			// This means we found a solution.
			return s.state.Solution()
		}
		if workingIndex < 0 {
			// No solution was found.
			return nil
		}

		entry := s.state.workingQueue[workingIndex]
		cont := solverContinuation{}
		if len(s.state.continuations) == workingIndex+1 {
			// We have a continuation for this working entry.
			// Use it.
			cont = s.state.continuations.Pop()
		}
		success, cont, undo := s.solveDep(entry, cont)
		if success {
			workingIndex++
			s.state.continuations.Push(cont)
			s.state.undos.Push(undo)
		} else {
			workingIndex--
			if len(s.state.undos) != 0 {
				undo := s.state.undos.Pop()
				s.applyUndo(undo)
			}
		}
	}
}

func (ss solverState) Solution() *Solution {
	result := Solution{
		pkgs:   map[string][]StringVersion{},
		minSDK: ss.minSDK,
	}
	for urlMajor, v := range ss.pkgs {
		url := urlMajor[:strings.LastIndex(urlMajor, "-")]
		result.pkgs[url] = append(result.pkgs[url], StringVersion{
			vStr: v.String(),
			v:    v,
		})
	}
	return &result
}

// versionFor returns the concrete version of the package url with the given constraintsString.
func (sol Solution) versionFor(url string, constraintsString string, ui UI) (string, error) {
	versions, ok := sol.pkgs[url]
	if !ok {
		return "", fmt.Errorf("package solution missing package '%s'", url)
	}
	constraints := version.Constraints{}
	if constraintsString != "" {
		var err error
		constraints, err = parseConstraint(constraintsString)
		if err != nil {
			return "", err
		}
	}
	// TODO(florian): we are parsing the version multiple times, and are running
	// through all existing versions multiple times. This can be optimized.
	for _, stringVersion := range versions {
		if constraints.Check(stringVersion.v) {
			return stringVersion.vStr, nil
		}
	}
	return "", fmt.Errorf("package solution missing target for '%s' with constraint '%s'", url, constraintsString)
}
