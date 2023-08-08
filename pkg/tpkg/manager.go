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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/toitlang/tpkg/pkg/set"
	"github.com/toitlang/tpkg/pkg/tracking"
)

type ProjectPaths struct {
	// Project root.
	ProjectRootPath string

	// The path of the lock file for the current project.
	LockFile string

	// The path of the spec file for the current project.
	SpecFile string
}

// Manager serves as entry point for all package-management related operations.
// Use NewManager to create a new manager.
type Manager struct {
	// The loaded registries.
	registries Registries

	// The package cache.
	cache Cache

	// The UI to communicate with the user.
	ui UI

	// The version of the current SDK. Used to select which packages are OK.
	// May be nil, in which case all packages are acceptable.
	sdkVersion *version.Version

	track tracking.Track
}

// ProjectPackageManager: a package manager for a specific project.
type ProjectPkgManager struct {
	*Manager

	// The project relevant Paths.
	Paths *ProjectPaths
}

// DescRegistry combines a description with the registry it comes from.
type DescRegistry struct {
	Desc     *Desc
	Registry Registry
}

type DescRegistries []DescRegistry

// NewManager returns a new Manager.
func NewManager(registries Registries, cache Cache, sdkVersion *version.Version, ui UI, track tracking.Track) *Manager {
	return &Manager{
		registries: registries,
		cache:      cache,
		ui:         ui,
		sdkVersion: sdkVersion,
		track:      track,
	}
}

func NewProjectPkgManager(manager *Manager, paths *ProjectPaths) *ProjectPkgManager {
	return &ProjectPkgManager{
		Manager: manager,
		Paths:   paths,
	}
}

// download fetches the given url/version, unless it's already in the cache.
func (m *ProjectPkgManager) download(ctx context.Context, url string, version string, hash string) error {
	projectRoot := m.Paths.ProjectRootPath
	packagePath, err := m.cache.FindPkg(projectRoot, url, version)
	if err != nil {
		return err
	}
	if packagePath != "" {
		return nil
	}
	err = m.cache.CreatePackagesCacheDir(projectRoot, m.ui)
	if err != nil {
		return err
	}
	p := m.cache.PreferredPkgPath(projectRoot, url, version)
	_, err = DownloadGit(ctx, DownloadGitOptions{
		Directory:  p,
		URL:        url,
		Version:    version,
		Hash:       hash,
		UI:         m.ui,
		NoReadOnly: false,
	})

	event := &tracking.Event{
		Name: "toit pkg download-git",
		Properties: map[string]string{
			"url":     url,
			"version": version,
			"hash":    hash,
		},
	}
	if err != nil {
		event.Properties["error"] = err.Error()
	}
	m.track(ctx, event)

	return err
}

func (m *ProjectPkgManager) downloadLockFilePackages(ctx context.Context, lf *LockFile) error {
	encounteredError := false
	for pkgID, pe := range lf.Packages {
		if pe.Path == "" {
			if err := m.download(ctx, pe.URL.URL(), pe.Version, pe.Hash); err != nil {
				return err
			}
			continue
		}
		// Just check that the path is actually there and is a directory.
		local := pe.Path.FilePath()
		isDir, err := isDirectory(local)
		if !isDir {
			m.ui.ReportError("Target of '%s' not a directory: '%s'", pkgID, local)
			encounteredError = true
		}
		if err != nil {
			return err
		}
	}
	if encounteredError {
		return ErrAlreadyReported
	}
	return nil
}

// pkgInstallRequest defines a package that should be installed.
// When a user initiates `pkg install foo`, this structure constains the
// information about which package exactly should be installed.
type pkgInstallRequest struct {
	name        string
	url         string
	major       int
	constraints string
}

// identifyInstallURL finds the package with the given pkgName.
// Returns the URL, its major version, and a version constraint if the pkgName had one.
func (m *ProjectPkgManager) identifyInstallURL(ctx context.Context, pkgName string) (*pkgInstallRequest, error) {
	if pkgName == "" {
		return nil, m.ui.ReportError("Missing package name")
	}

	var versionStr *string = nil
	if atPos := strings.LastIndexByte(pkgName, '@'); atPos > 0 {
		v := pkgName[atPos+1:]
		versionStr = &v
		pkgName = pkgName[:atPos]
	}

	var constraints version.Constraints
	if versionStr != nil {
		if *versionStr == "" {
			return nil, m.ui.ReportError("Missing version after '@' in '%s@'", pkgName)
		}
		var err error
		constraints, err = parseInstallConstraint(*versionStr)
		if err != nil {
			return nil, m.ui.ReportError("Invalid version: '%s'", *versionStr)
		}
	}

	// Always search for shortened URLs.
	found, err := m.registries.searchShortURL(pkgName)
	if err != nil {
		return nil, err
	}

	if !strings.Contains(pkgName, "/") {
		// Also search for the name.
		foundNames, err := m.registries.MatchName(pkgName)
		if err != nil {
			return nil, err
		}
		// Copying append.
		n := len(found)
		found = append(found[:n:n], foundNames...)
	}

	if len(found) == 0 {
		return nil, m.ui.ReportError("Package '%s' not found", pkgName)
	}

	urlCandidates := set.String{}
	for _, dr := range found {
		urlCandidates.Add(dr.Desc.URL)
	}

	url := found[0].Desc.URL

	if len(urlCandidates) > 1 {
		// Make one last attempt: if there is a package with the exact URL match, then we ignore
		// the other packages. In theory someone could have a bad name (although registries should
		// not accept them), or a URL could end with a full URL. For example: attack.com/github.com/real_package
		foundFullMatch := false
		for _, descReg := range found {
			if descReg.Desc.URL == pkgName {
				url = descReg.Desc.URL
				foundFullMatch = true
				break
			}
		}
		if !foundFullMatch {
			// TODO(florian): print all matching packages.
			return nil, m.ui.ReportError("More than one matching package '%s' found", pkgName)
		}
	}

	candidates, err := m.registries.SearchURL(url)
	if err != nil {
		return nil, err
	}

	var maxVersion *version.Version
	name := ""
	for _, candidate := range candidates {
		desc := candidate.Desc
		v, err := version.NewVersion(desc.Version)
		if err != nil {
			return nil, err
		}
		if constraints != nil && !constraints.Check(v) {
			continue
		}
		if maxVersion == nil || v.GreaterThan(maxVersion) {
			maxVersion = v
			name = desc.Name
		}
	}

	if maxVersion == nil {
		return nil, m.ui.ReportError("No package '%s' with version %s found", pkgName, *versionStr)
	}

	constraintsStr := ""
	if constraints != nil {
		constraintsStr = constraints.String()
	}
	return &pkgInstallRequest{
		url:         url,
		major:       maxVersion.Segments()[0],
		name:        name,
		constraints: constraintsStr,
	}, nil
}

func (m *ProjectPkgManager) readSpecAndLock() (*Spec, *LockFile, error) {
	lfPath := m.Paths.LockFile
	lfExists, err := isFile(lfPath)
	if err != nil {
		return nil, nil, err
	}

	specPath := m.Paths.SpecFile

	specExists, err := isFile(specPath)
	if err != nil {
		return nil, nil, err
	}

	var spec *Spec
	if specExists {
		spec, err = ReadSpec(specPath, m.ui)
		if err != nil {
			return nil, nil, err
		}
	}

	var lf *LockFile
	if lfExists {
		lf, err = ReadLockFile(lfPath)
		if err != nil {
			return nil, nil, err
		}
	}

	if lfExists && specExists {
		// Do a check to ensure that the lockfile is correct. We don't want
		// to overwrite/discard the lockfile if someone just creates an empty
		// spec file.

		missingPrefixes := []string{}
		for prefix := range lf.Prefixes {
			if _, ok := spec.Deps[prefix]; !ok {
				missingPrefixes = append(missingPrefixes, prefix)
			}
		}
		if len(missingPrefixes) == 1 {
			return nil, nil, m.ui.ReportError("Lock file has prefix that isn't in package.yaml: '%s'", missingPrefixes[0])
		} else if len(missingPrefixes) > 1 {
			sort.Strings(missingPrefixes)
			return nil, nil, m.ui.ReportError("Lock file has prefixes that aren't in package.yaml: %s", strings.Join(missingPrefixes, ", "))
		}
	}

	if !specExists {
		if lfExists {
			spec, err = NewSpecFromLockFile(lf)
			if err != nil {
				return nil, nil, err
			}
		} else {
			spec = newSpec(specPath)
		}
	}
	return spec, lf, nil
}

func (m *ProjectPkgManager) writeSpecAndLock(spec *Spec, lf *LockFile) error {
	err := spec.WriteToFile()
	if err != nil {
		return err
	}

	return lf.WriteToFile()
}

// InstallLocalPkg installs the local package at the given path.
// When provided, the package is installed with the given name. Otherwise, the
// packages name is extracted from the path.
// Returns the name that was used for the package.
// TODO(florian): the package name should be extracted from the package.yaml, or the README.
func (m *ProjectPkgManager) InstallLocalPkg(ctx context.Context, name string, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if isDir, err := isDirectory(abs); !isDir || err != nil {
		if err == nil {
			return "", m.ui.ReportError("Target '%s' is not a directory", path)
		} else if os.IsNotExist(err) {
			return "", m.ui.ReportError("Target '%s' does not exist", path)
		}
		return "", m.ui.ReportError("Target '%s' is not a directory: %v", path, err)
	}

	if name == "" {
		target_spec := filepath.Join(abs, "package.yaml")
		if isFile, err := isFile(target_spec); !isFile || err != nil {
			if err == nil {
				if !doesPathExist(target_spec) {
					return "", m.ui.ReportError("Missing 'package.yaml' in '%s'", path)
				}
				return "", m.ui.ReportError("Cannot read 'package.yaml' at '%s': not a file", path)
			}
			return "", m.ui.ReportError("Cannot read 'package.yaml' at '%s': %v", path, err)
		}
		spec, err := ReadSpec(target_spec, m.ui)
		if err != nil {
			return "", m.ui.ReportError("Cannot read 'package.yaml' at '%s': %v", target_spec, err)
		}
		if spec.Name == "" {
			return "", m.ui.ReportError("Missing name in 'package.yaml' of package at '%s'", path)
		}
		name = spec.Name
		if !isValidName(name) {
			return "", m.ui.ReportError("Invalid name '%s' in 'package.yaml' file at '%s'", name, path)
		}
	} else {
		if !isValidName(name) {
			return "", m.ui.ReportError("Invalid name: '%s'", name)
		}
	}

	spec, lf, err := m.readSpecAndLock()
	if err != nil {
		return "", err
	}

	if _, ok := spec.Deps[name]; ok {
		return "", m.ui.ReportError("Project has already a package with name '%s'", name)

	}

	// Add the local dependency to the deps before we build the solver deps.
	spec.addDep(name, "", "", path, m.ui)

	solverDeps, err := spec.BuildSolverDeps(m.ui)
	if err != nil {
		return "", err
	}

	solution, err := m.findSolution(spec.Environment.SDK, solverDeps, lf, nil)
	if err != nil {
		return "", err
	}
	if solution == nil {
		return "", m.ui.ReportError("Couldn't find a valid solution for the package constraints")
	}

	// Note that we need the downloaded packages, as we need their spec files to build
	// the updated lock file. Otherwise we don't have the prefixes of the packages.
	if err := m.downloadSolution(ctx, solution); err != nil {
		return "", err
	}

	updatedLock, err := spec.BuildLockFile(solution, m.cache, m.registries, m.ui)
	if err != nil {
		return "", err
	}

	err = m.writeSpecAndLock(spec, updatedLock)
	if err != nil {
		return "", err
	}

	return name, nil
}

// InstallURLPkg install the package identified by its identifier id.
// The id can be a (suffix of a) package URL, or a package name. The identifier
// can also be suffixed by a `@` followed by a version.
// When provided, the package is installed with the given name. Otherwise, the
// packages name (extracted from the description) is used.
// Returns (name, package-string, err).
func (m *ProjectPkgManager) InstallURLPkg(ctx context.Context, name string, id string) (string, string, error) {

	nameIsInferred := name == ""

	installPkg, err := m.identifyInstallURL(ctx, id)
	if err != nil {
		return "", "", err
	}

	if name == "" {
		name = installPkg.name
	}

	if !isValidName(name) {
		return "", "", m.ui.ReportError("Invalid name: '%s'", name)
	}

	spec, lf, err := m.readSpecAndLock()
	if err != nil {
		return "", "", err
	}

	// Packages can theoretically change names with different versions.
	// We will recheck before adding the new dependency.
	if _, ok := spec.Deps[name]; ok {
		return "", "", m.ui.ReportError("Project has already a package with name '%s'", name)

	}

	// Create the solver deps first and then only add a new dependency.
	// This is, because we don't yet know the exact version and name of the new
	// package.
	solverDeps, err := spec.BuildSolverDeps(m.ui)
	if err != nil {
		return "", "", err
	}
	solverDep, err := NewSolverDep(installPkg.url, installPkg.constraints)
	if err != nil {
		return "", "", err
	}
	solverDeps = append(solverDeps, solverDep)

	var unpreferred *PackageEntry
	// If the lock-file already contains an entry of this url-major, unprefer it, so we
	// get the latest one.
	if lf != nil {
		for _, pkg := range lf.Packages {
			if pkg.URL.URL() == installPkg.url {
				v, err := version.NewVersion(pkg.Version)
				if err != nil {
					return "", "", err
				}
				if installPkg.major == v.Segments()[0] {
					unpreferred = &pkg
					break
				}
			}
		}
	}

	solution, err := m.findSolution(spec.Environment.SDK, solverDeps, lf, unpreferred)
	if err != nil {
		return "", "", err
	}
	if solution == nil {
		return "", "", m.ui.ReportError("Couldn't find a valid solution for the package constraints")
	}

	// We still need to add the package to the dependencies.
	// Also, if the name was inferred, we need to check that the name is still the
	// right one, and check that we don't override an existing name.
	solvedVersion, err := solution.versionFor(installPkg.url, installPkg.constraints, m.ui)
	if err != nil {
		return "", "", err
	}

	if nameIsInferred {
		// Packages might change their name. This could change their preferred name.
		descReg, err := m.registries.SearchURLVersion(installPkg.url, solvedVersion)
		if err != nil {
			return "", "", err
		}
		if len(descReg) == 0 {
			return "", "", fmt.Errorf("couldn't find package '%s-%s' in registries", installPkg.url, solvedVersion)
		}
		if descReg[0].Desc.Name != name {
			m.ui.ReportInfo("Package '%s' has different names with different versions ('%s', '%s')", installPkg.url, name, descReg[0].Desc.Name)
			// The name of the package isn't the same as the one we expected.
			// We don't need to check if the name already exists, as adding it (with `addDep`) will
			// do that for us.
			name = descReg[0].Desc.Name
		}

	}
	// The installation process automatically adjusts the version constraint of
	// installed packages to accept semver compatible versions.
	versionConstraint := "^" + solvedVersion
	if err := spec.addDep(name, installPkg.url, versionConstraint, "", m.ui); err != nil {
		return "", "", err
	}

	// Note that we need the downloaded packages, as we need their spec files to build
	// the updated lock file. Otherwise we don't have the prefixes of the packages.
	if err := m.downloadSolution(ctx, solution); err != nil {
		return "", "", err
	}

	updatedLock, err := spec.BuildLockFile(solution, m.cache, m.registries, m.ui)
	if err != nil {
		return "", "", err
	}

	err = m.writeSpecAndLock(spec, updatedLock)
	if err != nil {
		return "", "", err
	}

	installedPkgStr := installPkg.url + "@" + solvedVersion
	return name, installedPkgStr, nil
}

func (m *ProjectPkgManager) Uninstall(ctx context.Context, name string) error {
	spec, lf, err := m.readSpecAndLock()
	if err != nil {
		return err
	}
	if _, ok := spec.Deps[name]; !ok {
		m.ui.ReportInfo("Package '%s' does not exist", name)
		return nil
	}
	delete(spec.Deps, name)

	updatedLock, err := m.solveAndDownload(ctx, spec, lf)
	if err != nil {
		return err
	}
	return m.writeSpecAndLock(spec, updatedLock)
}

// Install downloads all dependencies.
// Simply downloads all dependencies, if forceRecompute is false, and a lock file
// without local dependencies exists.
// Otherwise (re)computes the lockfile, giving preference to versions that are
// listed in the lockfile (if it exists).
func (m *ProjectPkgManager) Install(ctx context.Context, forceRecompute bool) error {
	spec, lf, err := m.readSpecAndLock()
	if err != nil {
		return err
	}

	needsToSolve := false
	if forceRecompute || lf == nil {
		needsToSolve = true
	} else if spec.Environment.SDK != lf.SDK {
		needsToSolve = true
	} else {
		for _, pkg := range lf.Packages {
			if pkg.Path != "" {
				// Path dependencies might have changed constraints.
				// Recompute the dependencies, preferring the existing entries.
				needsToSolve = true
				break
			}
		}
	}

	if !needsToSolve {
		return m.downloadLockFilePackages(ctx, lf)
	}

	updatedLock, err := m.solveAndDownload(ctx, spec, lf)
	if err != nil {
		return err
	}

	// We only need to write the lock file, because the spec
	// file hasn't changed. This has the really important added
	// benefit that we avoid read/write contention on the spec
	// files that are accessed through paths. Without this, it
	// is easy to run into reading partially written specs when
	// installing dependencies in parallel across multiple
	// projects.
	return updatedLock.WriteToFile()
}

func (m *ProjectPkgManager) Update(ctx context.Context) error {
	spec, _, err := m.readSpecAndLock()
	if err != nil {
		return err
	}

	updatedLock, err := m.solveAndDownload(ctx, spec, nil)
	if err != nil {
		return err
	}

	// Update the deps in the spec file with the new requirements.
	newSpec, err := NewSpecFromLockFile(updatedLock)
	if err != nil {
		return err
	}

	spec.Deps = newSpec.Deps

	return m.writeSpecAndLock(spec, updatedLock)
}

func (m *ProjectPkgManager) findSolution(minSDKStr string, solverDeps []SolverDep, oldLock *LockFile, unpreferred *PackageEntry) (*Solution, error) {
	solver, err := NewSolver(m.registries, m.sdkVersion, m.ui)
	if err != nil {
		return nil, err
	}
	if oldLock != nil {
		preferred := []versionedURL{}
		for _, lockPkg := range oldLock.Packages {
			if unpreferred != nil && lockPkg.URL == unpreferred.URL && lockPkg.Version == unpreferred.Version {
				continue
			}
			if lockPkg.URL != "" {
				preferred = append(preferred, versionedURL{
					URL:     lockPkg.URL.URL(),
					Version: lockPkg.Version,
				})
			}
		}
		solver.SetPreferred(preferred)
	}
	minSDK, err := sdkConstraintToMinSDK(minSDKStr)
	if err != nil {
		return nil, err
	}
	return solver.Solve(minSDK, solverDeps), nil
}

func (m *ProjectPkgManager) findSolutionFromSpec(spec *Spec, oldLock *LockFile) (*Solution, error) {
	solverDeps, err := spec.BuildSolverDeps(m.ui)
	if err != nil {
		return nil, err
	}
	return m.findSolution(spec.Environment.SDK, solverDeps, oldLock, nil)

}

// downloadSolution downloads all packages in the given solution.
func (m *ProjectPkgManager) downloadSolution(ctx context.Context, solution *Solution) error {
	for url, versions := range solution.pkgs {
		for _, version := range versions {
			// If we can't find the hash in the registries, we just use the empty string.
			hash, _ := m.registries.hashFor(url, version.vStr)
			err := m.download(ctx, url, version.vStr, hash)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// solveAndDownload takes the current spec file and downloads all dependencies.
// It uses the old lockfile as hints for which package versions are preferred.
// Returns a lock-file corresponding to the resolved packages of the spec.
func (m *ProjectPkgManager) solveAndDownload(ctx context.Context, spec *Spec, oldLock *LockFile) (*LockFile, error) {
	solution, err := m.findSolutionFromSpec(spec, oldLock)
	if err != nil {
		return nil, err
	}
	if solution == nil {
		return nil, m.ui.ReportError("Couldn't find a valid solution for the package constraints")
	}
	// Note that we need the downloaded packages, as we need their spec files to build
	// the updated lock file. Otherwise we don't have the prefixes of the packages.
	if err := m.downloadSolution(ctx, solution); err != nil {
		return nil, err
	}
	return spec.BuildLockFile(solution, m.cache, m.registries, m.ui)
}

// CleanPackages removes unused downloaded packages from the local cache.
func (m *ProjectPkgManager) CleanPackages() error {
	_, lf, err := m.readSpecAndLock()
	if err != nil {
		return err
	}
	if lf == nil {
		lf = &LockFile{}
	}

	rootPath := m.Paths.ProjectRootPath
	fullProjectPkgsPath, err := filepath.Abs(m.cache.PkgInstallPath(rootPath))
	if err != nil {
		return err
	}
	stat, err := os.Stat(fullProjectPkgsPath)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	} else if !stat.IsDir() {
		return m.ui.ReportError("Packages cache path not a directory: '%s'", fullProjectPkgsPath)
	}

	// Build up a tree of segments so we can more efficiently
	// true: this is a full path, and no nested files must be removed.
	// false: this is a path that needs to be keep, but we must recurse.
	toKeep := map[string]bool{}
	for _, pkg := range lf.Packages {
		pkgPath, err := m.cache.FindPkg(rootPath, pkg.URL.URL(), pkg.Version)
		if err != nil {
			return err
		}
		if pkgPath != "" {
			fullPkgPath, err := filepath.Abs(pkgPath)
			if err != nil {
				return err
			}
			if strings.HasPrefix(fullPkgPath, fullProjectPkgsPath) {
				rel, err := filepath.Rel(fullProjectPkgsPath, fullPkgPath)
				if err != nil {
					return err
				}
				segments := strings.Split(rel, string(filepath.Separator))
				accumulated := ""
				for _, segment := range segments {
					if accumulated == "" {
						accumulated = segment
					} else {
						accumulated = filepath.Join(accumulated, segment)
					}
					toKeep[accumulated] = false
				}
				toKeep[accumulated] = true
			}
		}
	}
	// We now have all the project paths we want to keep.
	// Also add the README.md, that comes from the package manager.
	toKeep["README.md"] = false

	// Run through the cache directory and remove all the ones we don't need anymore.
	return filepath.Walk(fullProjectPkgsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == fullProjectPkgsPath {
			return nil
		}
		rel, err := filepath.Rel(fullProjectPkgsPath, path)
		if err != nil {
			return err
		}
		isFullPkgPath, ok := toKeep[rel]
		if !ok {
			err := os.RemoveAll(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if isFullPkgPath {
			return filepath.SkipDir
		}
		return nil
	})
}

// setupPaths sets the spec and lock file, searching in the given directory.
//
// Does not overwrite a set value (m.SpecFile or m.LockFile).
// If the given directory is empty, starts the search in the current working directory.
// If a file doesn't exists, returns the path for it in the given directory.
func NewProjectPaths(projectRoot string, lockPath string, specPath string) (*ProjectPaths, error) {

	if projectRoot != "" {
		if lockPath == "" {
			lockPath = lockPathForDir(projectRoot)
		}
		if specPath == "" {
			specPath = pkgPathForDir(projectRoot)
		}
		return &ProjectPaths{
			ProjectRootPath: projectRoot,
			LockFile:        lockPath,
			SpecFile:        specPath,
		}, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	startDir := dir
	lockPathCandidate := ""
	specPathCandidate := ""
	for {
		lockPathCandidate = lockPathForDir(dir)
		specPathCandidate = pkgPathForDir(dir)

		if info, err := os.Stat(lockPathCandidate); err == nil && !info.IsDir() {
			// Found the project root.
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		if info, err := os.Stat(specPathCandidate); err == nil && !info.IsDir() {
			// Found the project root.
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		// Prepare for the next iteration.
		// If there isn't one, we assume that the lock file and the spec file should
		// be in the starting directory.
		newDir := filepath.Dir(dir)
		if newDir == dir {
			dir = startDir
			lockPathCandidate = lockPathForDir(startDir)
			specPathCandidate = pkgPathForDir(startDir)
			break

		} else {
			dir = newDir
		}
	}

	if lockPath == "" {
		lockPath = lockPathCandidate
	}
	if specPath == "" {
		specPath = specPathCandidate
	}
	return &ProjectPaths{
		ProjectRootPath: dir,
		LockFile:        lockPath,
		SpecFile:        specPath,
	}, nil
}

// WithoutLowerVersions discards descriptions of packages where a higher
// version exists.
// If a constraint is given, then descriptions are first filtered out according to
// the constraint.
func (descs DescRegistries) WithoutLowerVersions() (DescRegistries, error) {
	if len(descs) == 0 {
		return descs, nil
	}

	sort.SliceStable(descs, func(p, q int) bool {
		a := descs[p]
		b := descs[q]
		return a.Desc.IDCompare(b.Desc) < 0
	})
	// Only keep the highest version of a package.
	to := 0
	for i := 1; i < len(descs); i++ {
		current := descs[i]
		previous := descs[i-1]
		if current.Desc.URL == previous.Desc.URL {
			// Same package. Maybe different version, but the latter is either higher or equal.
			descs[to] = current
		} else {
			to++
			descs[to] = current
		}
	}
	descs = descs[0 : to+1]
	return descs, nil
}
