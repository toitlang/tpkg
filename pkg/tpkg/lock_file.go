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
	"os"
	"path/filepath"
	"strings"

	"github.com/toitlang/tpkg/pkg/compiler"
	"gopkg.in/yaml.v2"
)

// A lock file is used by the compiler to find the sources of packages.
// It's the result of a package resolution where "vague" package
// dependencies are used to find concrete versions.
// It is common to share lock files (and to check them in), so that all
// developers work on the same version. As such they must not contain
// absolute paths.

// LockFile represents a lock file.
type LockFile struct {
	// The path to the lock file. If any.
	path string `yaml:"-"`
	// SDK constraint, if any.
	// Must be of form '^version'.
	SDK string `yaml:"sdk,omitempty"`
	// Prefixes for the entry module.
	Prefixes PrefixMap `yaml:"prefixes,omitempty"`
	// All dependent packages: from package-id to their PackageEntry
	Packages map[string]PackageEntry `yaml:"packages,omitempty"`
}

// PackageEntry corresponds to a resolved package.
// If 'path' is given, the package is at the location given by the path. The path
// can be absolute or relative to the lock file.
// If 'url' is given, then 'version' must be given as well. The entry then refers to
// a non-local package and is found in the package cache.
type PackageEntry struct {
	URL      compiler.URIPath `yaml:"url,omitempty"`
	Name     string           `yaml:"name,omitempty"`
	Version  string           `yaml:"version,omitempty"`
	Path     compiler.Path    `yaml:"path,omitempty"`
	Hash     string           `yaml:"hash,omitempty"`
	Prefixes PrefixMap        `yaml:"prefixes,omitempty"`
}

// PrefixMap has a mapping from prefix to package-id.
type PrefixMap map[string]string

// Validate ensures that the receiver is a valid LockFileEntry.
func (pe PackageEntry) Validate(ui UI) error {
	if pe.URL != "" && pe.Version == "" {
		return ui.ReportError("Invalid lock file: url without version")
	}
	if pe.URL == "" && pe.Path == "" {
		return ui.ReportError("Invalid lock file: missing 'url' and 'path'")
	}
	return nil
}

// ReadLockFile reads the lock-file at the given path.
func ReadLockFile(path string) (*LockFile, error) {
	// TODO(florian): should we validate the file here?
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var res LockFile
	if err := yaml.Unmarshal(b, &res); err != nil {
		return nil, err
	}

	res.path = path
	return &res, nil
}

func (lf *LockFile) WriteToFile() error {
	// Write the YAML to memory first, and then compare it with any
	// existing file.
	// We don't want to touch files if they don't change.

	b, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}

	return writeFileIfChanged(lf.path, b)
}

// PrintLockFile prints the contents of the lock file for the current project.
func (m *ProjectPkgManager) PrintLockFile() error {
	path := m.Paths.LockFile
	content, err := ReadLockFile(path)
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

func toValidPkgID(str string) string {
	isFirst := true
	return strings.Map(func(r rune) rune {
		tmp := isFirst
		isFirst = false
		if (!tmp && '0' <= r && r <= '9') ||
			('a' <= r && r <= 'z') ||
			('A' <= r && r <= 'Z') ||
			r == '_' || r == '-' || r == '.' || r == '/' {
			return r
		}
		return '_'
	}, str)
}

// buildIDSegments computes the segments of the ID.
// Generally, we prefer shorter IDs, and start from the back.
// We do some manipulations to ensure that the resulting segments
// yield valid package ids (when concatenated with "/"), and we
// remove the version for package IDs.
func (pe *PackageEntry) buildIDSegments() []string {
	// Package ids are used in error messages. As such, we want to have
	// ids that are representative of the package they represent.
	// Say, we have a package with path `github.com/company/project/1.2.3`.
	// We drop the version number. The caller should use the version number, if
	// it can detect that there are two packages that only differ with respect
	// to their version number.
	//
	// As long as we don't have ambiguities, we prefer:
	// - "project"
	// - "company/project",
	// - "github.com/company/project"
	//
	// Since a package and path can have the same path, we start prefixing a
	// path package with 'path-' at this point.
	// Note that this might not be enough to disambiguate, as we have to make
	// the IDs valid (see toValidPkgID). The caller must then change the ids to
	// make them unique.
	if pe.Path != "" {
		path := toValidPkgID(filepath.ToSlash(pe.Path.FilePath()))
		return strings.Split(path, "/")
	}
	url := toValidPkgID(pe.URL.URL())
	return strings.Split(url, "/")
}

func (lf *LockFile) optimizePkgIDs() {
	// Package ids are used in error messages. As such, we want to have
	// ids that are representative of the package they represent.
	// Say, we have a package with path `github.com/company/project/1.2.3`.
	// We drop the version number. The caller should use the version number, if
	// it can detect that there are two packages that only differ with respect
	// to their version number.
	//
	// As long as we don't have ambiguities, we prefer:
	// - "project" over
	// - "company/project", over
	// - "github.com/company/project"
	//
	// If there are two versions of the same package, we first disambiguate the
	// package from other packages, and then suffix the version.
	//
	// Any remaining ambiguity is resolved without trying to be clever. (Should
	// be rare).

	// Start by identifying packages that only differ in version.
	// These will be suffixed by their version number to disambiguate them.

	// Mapping from oldId to list of other old ids with different versions.
	differentVersionOf := map[string][]string{}
	// Map from URLs to the first old-id that used that url.
	pkgURLs := map[string]string{}

	// Path segments of all old-ids that need a new ID.
	// If a package exists in multiple versions, then only one representative old-ID for
	// this package is added. The other old-IDs can be found in 'differentVersionOf'.
	allSegments := map[string][]string{}

	for oldID, entry := range lf.Packages {
		if entry.Path != "" {
			allSegments[oldID] = entry.buildIDSegments()
			continue
		}
		url := entry.URL.URL()
		if seen, ok := pkgURLs[url]; ok {
			versionsOfSeen := differentVersionOf[oldID]
			// Copying append.
			n := len(versionsOfSeen)
			differentVersionOf[seen] = append(versionsOfSeen[:n:n], oldID)
		} else {
			allSegments[oldID] = entry.buildIDSegments()
			pkgURLs[url] = oldID
		}
	}

	newIDs := map[string][]string{}

	// Use more and more segments.
	for i := 1; len(allSegments) != 0; i++ {
		candidates := map[string][]string{}
		for oldID, segments := range allSegments {
			l := len(segments)
			var candidate string
			if i <= l {
				candidate = strings.Join(segments[l-i:l], "/")
			} else {
				// This shouldn't really happen. Only possible if there are empty segments.
				candidate = strings.Join(segments, "/")
			}
			candidates[candidate] = append(candidates[candidate], oldID)
		}
		// Go through the candidates and see if some are unique, or don't have
		// any more segments.
		for candidate, oldIDs := range candidates {
			isSingle := len(oldIDs) == 1
			for _, oldID := range oldIDs {
				segments := allSegments[oldID]
				if isSingle || i >= len(segments) {
					// Either the only one, or we can't do more than that.
					differentVersions, needsVersion := differentVersionOf[oldID]
					if needsVersion {
						// Copying append.
						n := len(differentVersions)
						allIDs := append(differentVersions[:n:n], oldID)
						for _, oldID := range allIDs {
							lfe := lf.Packages[oldID]
							newID := candidate + "-" + lfe.Version
							newIDs[newID] = append(newIDs[newID], oldID)
						}
					} else {
						newIDs[candidate] = append(newIDs[candidate], oldID)
					}
					delete(allSegments, oldID)
				}
			}
		}
	}

	finalIDs := map[string]string{}
	old2New := map[string]string{}

	// Go through the ids one last time to make sure they really are unique.
	// If they aren't disambiguate them. No attempt to be pretty anymore.

	// Copy over the IDs that are working. They have priority.
	for newID, oldIDs := range newIDs {
		if len(oldIDs) == 1 {
			finalIDs[newID] = oldIDs[0]
			old2New[oldIDs[0]] = newID
		}
	}
	// Now deal with ambiguity.
	for newID, oldIDs := range newIDs {
		if len(oldIDs) == 1 {
			continue
		}
		counter := 0
		for _, oldID := range oldIDs {
			for {
				candidate := fmt.Sprintf("%s--%d", newID, counter)
				counter++
				if _, ok := finalIDs[candidate]; !ok {
					finalIDs[candidate] = oldID
					old2New[oldID] = candidate
					break
				}
			}
		}
	}

	// Check that all package IDs have a new ID. If not, then we have
	// a prefix that uses a package ID that isn't present. In that case we
	// simply don't optimize.

	for _, oldID := range lf.Prefixes {
		if _, ok := old2New[oldID]; !ok {
			return
		}
	}
	for _, pe := range lf.Packages {
		for _, oldID := range pe.Prefixes {
			if _, ok := old2New[oldID]; !ok {
				return
			}
		}
	}

	// All IDs can be mapped. Do that now.

	// Local function to map prefixes.
	buildNewPrefixes := func(prefixes PrefixMap) PrefixMap {
		if prefixes == nil {
			return nil
		}
		result := PrefixMap{}
		for prefix, oldId := range prefixes {
			result[prefix] = old2New[oldId]
		}
		return result
	}

	newPackages := map[string]PackageEntry{}

	for oldID, entry := range lf.Packages {
		newID := old2New[oldID]
		entry.Prefixes = buildNewPrefixes(entry.Prefixes)
		newPackages[newID] = entry
	}

	lf.Packages = newPackages
	lf.Prefixes = buildNewPrefixes(lf.Prefixes)
}
