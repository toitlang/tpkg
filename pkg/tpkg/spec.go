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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/toitlang/tpkg/pkg/compiler"
	"github.com/toitlang/tpkg/pkg/set"
	"gopkg.in/yaml.v2"
)

// Spec specifies a Package.
// This specification is used for two (partially overlapping) purposes:
// 1. create a package description for package registries.
// 2. specify the prefix-dependency mapping.
type Spec struct {
	path        string          `yaml:"-"`
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	License     string          `yaml:"license,omitempty"`
	Environment SpecEnvironment `yaml:"environment,omitempty"`
	Deps        DependencyMap   `yaml:"dependencies,omitempty"`
}

type SpecEnvironment struct {
	SDK string `yaml:"sdk,omitempty"`
}

// DependencyMap is a map from prefix to package.
type DependencyMap map[string]SpecPackage

// SpecPackage identifies a package in the spec file.
// A valid instance has at least URL or Path set.
type SpecPackage struct {
	// URL is the github URL (for cloning) of the package.
	// It uniquely identifies the package (all its versions) in the registry.
	URL string `yaml:"url,omitempty"`
	// Version is a version constraint on the package.
	// A missing version constraint allows any version of the package.
	Version string `yaml:"version,omitempty"`
	// Path is set if the package should be found locally.
	// This field overrides all other fields. This makes it possible to
	// temporarily (during development) switch to a local version.
	Path compiler.Path `yaml:"path,omitempty"`
}

// TODO (jesper): Parse and WriteYAML should preserve comments.
func (s *Spec) Parse(b []byte, ui UI) error {
	if err := yaml.Unmarshal(b, s); err != nil {
		return ui.ReportError("Failed to parse app specification: %w", err)
	}

	if err := s.Validate(ui); err != nil {
		if !IsErrAlreadyReported(err) {
			return ui.ReportError("Failed to parse app specification: %w", err)
		}
		return err
	}

	return nil
}

func (s *Spec) ParseString(str string, ui UI) error {
	return s.Parse([]byte(str), ui)
}

func (s *Spec) Validate(ui UI) error {
	if s.Name != "" && !isValidName(s.Name) {
		return ui.ReportError("Invalid name: '%s'", s.Name)
	}
	for prefix, dep := range s.Deps {
		if err := validatePrefix(prefix, ui); err != nil {
			return err
		}
		if err := dep.Validate(prefix, ui); err != nil {
			return err
		}
	}
	if s.Environment.SDK != "" {
		sdk := s.Environment.SDK
		if !strings.HasPrefix(sdk, "^") {
			return ui.ReportError("SDK constraint must be of form '^version': '%s'", sdk)
		}
		_, err := parseConstraintRange(sdk[1:], semverRange)
		if err != nil {
			return ui.ReportError("Invalid SDK constraint '%s'", sdk)
		}
	}
	return nil
}

func (s *Spec) ParseFile(filename string, ui UI) error {
	s.path = filename
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	return s.Parse(b, ui)
}

// ReadSpec reads the spec-file at the given path.
func ReadSpec(path string, ui UI) (*Spec, error) {
	spec := Spec{}
	err := spec.ParseFile(path, ui)
	if err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *Spec) WriteYAML(writer io.Writer) error {
	return yaml.NewEncoder(writer).Encode(s)
}

func (s *Spec) WriteToFile() error {
	file, err := os.Create(s.path)
	if err != nil {
		return err
	}
	defer func() {
		e := file.Close()
		if e != nil {
			err = e
		}
	}()

	return s.WriteYAML(file)
}

// BuildLockFile generates a lock file using the given solution.
// Assumes that all packages in the solution are used.
func (s *Spec) BuildLockFile(solution *Solution, cache Cache, registries Registries, ui UI) (*LockFile, error) {
	lockPath := filepath.Join(filepath.Dir(s.path), DefaultLockFileName)
	sdkMin := ""
	if solution.minSDK != nil {
		sdkMin = "^" + solution.minSDK.String()
	}
	result := LockFile{
		path:     lockPath,
		SDK:      sdkMin,
		Prefixes: nil, // Will be overwritten.
		Packages: map[string]PackageEntry{},
	}

	// Map from URL/version to pkg-id.
	pkgIDs := map[string]map[string]string{}
	idCounter := 0
	for url, versions := range solution.pkgs {
		pkgIDs[url] = map[string]string{}
		for _, version := range versions {
			pkgIDs[url][version.vStr] = fmt.Sprintf("pkg%d", idCounter)
			idCounter++
		}
	}

	// buildPrefixes only handles uri dependencies.
	// Local dependencies are done in a separate step.
	buildPrefixes := func(spec *Spec, localDepsAllowed bool) (PrefixMap, error) {
		if spec == nil {
			return PrefixMap{}, nil
		}
		dm := spec.Deps
		prefixes := PrefixMap{}
		for prefix, specPkg := range dm {
			if specPkg.Path != "" {
				if !localDepsAllowed {
					return nil, ui.ReportError("Path dependency '%s: %s'not allowed in '%s'", prefix, specPkg.Path, spec.path)
				}
				// Local dependencies are done later.
				continue
			}
			version, err := solution.versionFor(specPkg.URL, specPkg.Version, ui)
			if err != nil {
				return nil, err
			}
			prefixes[prefix] = pkgIDs[specPkg.URL][version]
		}
		return prefixes, nil
	}

	// We assume that the current specification is the "entry" specification.
	entryPrefixes, err := buildPrefixes(s, true)
	if err != nil {
		return nil, err
	}
	result.Prefixes = entryPrefixes

	for url, versions := range pkgIDs {
		for version, pkgID := range versions {
			projectPath := filepath.Dir(s.path)
			specPath, err := cache.SpecPathFor(projectPath, url, version)
			if err != nil {
				return nil, err
			}
			depSpec, err := ReadSpec(specPath, ui)
			if err != nil {
				return nil, err
			}
			name := depSpec.Name
			if name == "" {
				// The spec file doesn't have the name inside yet.
				// Use the one from the registry.
				// This should only fail for local dependencies.
				name, _ = registries.nameFor(url, version)
			}
			prefixes, err := buildPrefixes(depSpec, true)
			if err != nil {
				return nil, err
			}
			// If we can't find the hash we just use "".
			hash, _ := registries.hashFor(url, version)
			result.Packages[pkgID] = PackageEntry{
				URL:      compiler.ToURIPath(url),
				Name:     name,
				Version:  version,
				Hash:     hash,
				Prefixes: prefixes,
			}
		}
	}

	// Local pkgs are only allowed in the entry packager, or in local packages that
	// have been referenced through local dependencies. As such, a `visitLocalDeps`
	// captures all of them.
	localPkgIDs := map[string]string{}

	addLocalDependencies := func(s *Spec, prefixes PrefixMap) {
		dir := filepath.Dir(s.path)
		// Go through the dependencies again and find local deps.
		for prefix, specPkg := range s.Deps {
			if specPkg.Path == "" {
				continue
			}
			p := specPkg.Path.FilePath()
			fullPath := p
			if !filepath.IsAbs(fullPath) {
				fullPath = filepath.Join(dir, p)
			}
			fullPath = filepath.Clean(fullPath)
			targetID, ok := localPkgIDs[fullPath]
			if !ok {
				targetID = fmt.Sprintf("localPkg%d", idCounter)
				localPkgIDs[fullPath] = targetID
				idCounter++
			}
			prefixes[prefix] = targetID
		}
	}

	addLocalDependencies(s, entryPrefixes)

	err = s.visitLocalDeps(ui, func(pkgPath string, fullPath string, depSpec *Spec) error {
		if pkgPath == "" {
			// Entry spec is already done.
			return nil
		}
		pkgID, ok := localPkgIDs[fullPath]
		// When the package was used as a target for a dependency we already added the id.
		if !ok {
			// First time we see this local package.
			pkgID = fmt.Sprintf("localPkg%d", idCounter)
			localPkgIDs[fullPath] = pkgID
			idCounter++
		}
		prefixes, err := buildPrefixes(depSpec, true)
		if err != nil {
			return err
		}

		if depSpec != nil {
			addLocalDependencies(depSpec, prefixes)
		}
		result.Packages[pkgID] = PackageEntry{
			Path:     compiler.ToPath(pkgPath),
			Prefixes: prefixes,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	result.optimizePkgIDs()
	return &result, nil
}

// visitLocalDeps visits all local dependencies of this spec.
// Starts by invoking the callback for the given spec, with pkgPath "".
// Then: for each local dependency it invokes the callback 'cb' with the path of the package,
// and its spec file. The 'depSpec' may be nil if the package doesn't have a spec file.
// Note that the callback is never called multiple times for the same spec-file, even if a
// package can be reached through different paths. That is, a local dependency with
// an absolute path and a local dependency with a relative path are treated as being the same,
// if they end up in the same place.
// If the callback returns an error, aborts with that error.
// The 'pkgPath' is the path how the package was found. It may be absolute or relative (to
// this specification).
func (s *Spec) visitLocalDeps(ui UI, cb func(pkgPath string, fullPath string, depSpec *Spec) error) error {
	entryDir := filepath.Clean(filepath.Dir(s.path))
	alreadyVisited := set.String{}
	var visit func(spec *Spec) error
	visit = func(spec *Spec) error {
		for _, dep := range spec.Deps {
			if dep.Path == "" {
				continue
			}

			pkgPath := dep.Path.FilePath()
			if !filepath.IsAbs(pkgPath) && spec != s {
				pkgPath = filepath.Join(filepath.Dir(spec.path), pkgPath)
			}
			// We ensure that all specs are relative to the 's' spec, or absolute.
			// At this point, the pkgPath is thus also relative to 's' or absolute.
			pkgPath = filepath.Clean(pkgPath)
			if strings.HasPrefix(pkgPath, "..") {
				// See if we can further simplify the pkgPath. If it starts with dots, but
				// actually dots out and in of its own directory, we can simplify.
				// For example, a path '../foo/bar' inside a folder 'foo' can be simplified
				// to 'bar'.
				s_dir := filepath.Dir(s.path)
				relPath, err := filepath.Rel(s_dir, filepath.Join(s_dir, pkgPath))
				if err == nil {
					pkgPath = relPath
				}
			}

			// 'filepath.Rel' sometimes adds '/.' to the path, which we don't want.
			// Generally, just remove any trailing '/.'.
			slashDot := string(filepath.Separator) + "."
			if pkgPath != slashDot {
				pkgPath = strings.TrimSuffix(pkgPath, slashDot)
			}

			fullPath := pkgPath
			if !filepath.IsAbs(fullPath) {
				fullPath = filepath.Join(entryDir, fullPath)
				fullPath = filepath.Clean(fullPath)
			}
			specPath := filepath.Join(fullPath, DefaultSpecName)

			if alreadyVisited.Contains(fullPath) {
				continue
			}
			alreadyVisited.Add(fullPath)
			exists, err := isFile(specPath)
			if err != nil {
				return err
			}
			// Local-packages are allowed not to have a spec file.
			if exists {
				depSpec, err := ReadSpec(specPath, ui)
				if err != nil {
					return err
				}
				err = cb(pkgPath, fullPath, depSpec)
				if err != nil {
					return err
				}
				depSpec.path = filepath.Join(pkgPath, DefaultSpecName)
				// Recursively find more local specs.
				err = visit(depSpec)
				if err != nil {
					return err
				}
			} else {
				err = cb(pkgPath, fullPath, nil)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := cb("", entryDir, s)
	alreadyVisited.Add(entryDir)
	if err != nil {
		return err
	}
	return visit(s)
}

func (s *Spec) addDep(prefix string, url string, version string, p string, ui UI) error {
	if s.Deps == nil {
		// TODO(florian): we should probably just ensure that there always is a map when
		// creating or loading a spec.
		s.Deps = DependencyMap{}
	}
	if !isValidName(prefix) {
		return ui.ReportError("Invalid prefix: '%s'", prefix)
	}
	if _, ok := s.Deps[prefix]; ok {
		return ui.ReportError("Project has already a package with prefix '%s'", prefix)

	}

	s.Deps[prefix] = SpecPackage{
		URL:     url,
		Version: version,
		Path:    compiler.ToPath(p),
	}
	return nil
}

// TODO(florian): this function should be shared with the lock-file.
func isValidName(prefix string) bool {
	validID := regexp.MustCompile(`^[a-zA-Z_](-?[a-zA-Z0-9_])*$`)
	return validID.MatchString(prefix)
}

// TODO(florian): create a Prefix type.
func validatePrefix(prefix string, ui UI) error {
	if !isValidName(prefix) {
		return ui.ReportError("Invalid prefix: '%s'", prefix)
	}
	return nil
}

func (sp *SpecPackage) Validate(prefix string, ui UI) error {
	if sp.URL == "" && sp.Path == "" {
		return ui.ReportError("Package entry for prefix '%s' is missing 'url' or 'path'", prefix)
	}
	if sp.URL == "" && sp.Version != "" {
		ui.ReportWarning("Package entry for prefix '%s' has version constraint but no URL", prefix)
	}
	if sp.Version != "" {
		if _, err := parseConstraint(sp.Version); err != nil {
			return ui.ReportError("Package entry for prefix '%s' has invalid version constraint: '%s'", prefix, sp.Version)
		}
	}
	return nil
}

func (pe PackageEntry) toSpecPackage() SpecPackage {
	version := pe.Version
	if version != "" {
		version = "^" + version
	}
	sp := SpecPackage{
		URL:     pe.URL.URL(),
		Version: version,
		Path:    pe.Path,
	}
	return sp
}

func newSpec(specPath string) *Spec {
	return &Spec{
		path: specPath,
	}
}

func NewSpecFromLockFile(lf *LockFile) (*Spec, error) {
	specPath := filepath.Join(filepath.Dir(lf.path), DefaultSpecName)
	s := newSpec(specPath)

	deps := DependencyMap{}
	for prefix, pkgID := range lf.Prefixes {
		packageEntry, ok := lf.Packages[pkgID]
		if !ok {
			return &Spec{}, fmt.Errorf("missing package '%s' in lockfile", pkgID)
		}
		deps[prefix] = packageEntry.toSpecPackage()
	}
	s.Deps = deps
	return s, nil
}

// BuildSolverDeps builds an array of SolverDeps from the content of the lock file.
// This involves looking into spec files of local dependencies.
func (s *Spec) BuildSolverDeps(ui UI) ([]SolverDep, error) {
	result := []SolverDep{}
	err := s.visitLocalDeps(ui, func(pkgPath string, _ string, depSpec *Spec) error {
		if depSpec == nil {
			return nil
		}
		for _, dep := range depSpec.Deps {
			if dep.Path != "" {
				continue
			}
			solverDep, err := NewSolverDep(dep.URL, dep.Version)
			if err != nil {
				return err
			}
			result = append(result, solverDep)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// PrintSpecFile prints the contents of the spec file for the current project.
func (m *ProjectPkgManager) PrintSpecFile() error {
	path := m.Paths.SpecFile
	content, err := ReadSpec(path, m.ui)
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(content)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", b)
	return nil
}
