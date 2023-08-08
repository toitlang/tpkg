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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"gopkg.in/yaml.v2"
)

// Desc describes a Package.
// The description is used in registries.
// It contains all the information necessary to download and install a package.
// Furthermore, it contains all dependency information.
// Fundamentally, a description is sufficient to determine which versions a
// program wants to use, before downloading any source.
// The package resolution mechanism only needs descriptions.
type Desc struct {
	// The path of the description file, if any.
	path        string `yaml:"-" json:"-"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description"`

	License string `yaml:"license,omitempty" json:"license"`
	// We might want to add a url-kind in the future, but currently we
	// don't need it, and just assume that every url is a git location.
	URL         string          `yaml:"url" json:"url"`
	Version     string          `yaml:"version" json:"version"`
	Environment DescEnvironment `yaml:"environment,omitempty" json:"environment,omitempty"`

	// The git-hash of the package.
	Hash string `yaml:"hash,omitempty" json:"hash"`

	Deps []descPackage `yaml:"dependencies,omitempty" json:"dependencies"`
}

type DescEnvironment struct {
	SDK string `yaml:"sdk,omitempty" json:"sdk,omitempty"`
}

func NewDesc(name string, description string, url string, version string, sdk string, license string, hash string, deps []descPackage) *Desc {
	return &Desc{
		Name:        name,
		Description: description,
		Version:     version,
		Environment: DescEnvironment{
			SDK: sdk,
		},
		License: license,
		URL:     url,
		Hash:    hash,
		Deps:    deps,
	}
}

type descPackage struct {
	URL     string `yaml:"url" json:"url"`
	Version string `yaml:"version" json:"version"` // This is actually a constraint.
}

type AllowLocalDepsFlag int

const (
	AllowLocalDeps AllowLocalDepsFlag = iota
	ReportLocalDeps
	DisallowLocalDeps
)

// TODO (jesper): Parse and WriteTo should preserve comments.
func (d *Desc) Parse(b []byte, ui UI) error {
	fail := func(err error) error {
		if IsErrAlreadyReported(err) {
			return err
		}
		if d.path != "" {
			return ui.ReportError("Failed to parse package description '%s': %w", d.path, err)
		}
		return ui.ReportError("Failed to parse package description: %w", err)
	}

	if err := yaml.Unmarshal(b, d); err != nil {
		return fail(err)
	}

	if err := d.Validate(ui); err != nil {
		return fail(err)
	}

	v, err := version.NewVersion(d.Version)
	if err != nil {
		if d.path != "" {
			return ui.ReportError("Invalid version in '%s': %v", d.path, d.Version)
		}
		return ui.ReportError("Invalid version: %v", d.Version)
	}

	// Canonicalize the version.
	d.Version = v.String()

	for _, dep := range d.Deps {
		constraint := dep.Version
		_, err := parseConstraint(constraint)
		if err != nil {
			if d.path != "" {
				return ui.ReportError("Invalid constraint in '%s': %v", d.path, d.Version)
			}
			return ui.ReportError("Invalid constraint: %v", constraint)
		}
	}

	return nil
}

func (d *Desc) ParseString(str string, ui UI) error {
	return d.Parse([]byte(str), ui)
}

func (d *Desc) Validate(ui UI) error {
	// TODO(florian): verify that the name is a valid identifier?
	if d.Name == "" {
		if d.path != "" {
			return ui.ReportError("Description at '%s' is missing a name", d.path)
		}
		return ui.ReportError("Description is missing a name")
	}
	if d.Version == "" {
		return ui.ReportError("Description '%s' is missing a version", d.Name)
	}
	if d.URL == "" {
		return ui.ReportError("Specification '%s' has an empty URL", d.Name)
	}

	if d.Environment.SDK != "" {
		sdk := d.Environment.SDK
		if !strings.HasPrefix(sdk, "^") {
			return ui.ReportError("SDK constraint must be of form '^version': '%s'", sdk)
		}
		_, err := parseConstraintRange(sdk[1:], semverRange)
		if err != nil {
			return ui.ReportError("Invalid SDK constraint '%s'", sdk)
		}
	}

	// TODO(florian): enable this check.
	/*
		if !filepath.IsAbs(d.URL) && d.Hash == "" {
			ui.ReportWarning("Description '%s' (%s) is missing a hash", d.Name, d.URL)
		}
	*/
	return nil
}

func (d *Desc) ParseFile(filename string, ui UI) error {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	d.path = filename
	return d.Parse(b, ui)
}

func (d *Desc) WriteYAML(writer io.Writer) error {
	return yaml.NewEncoder(writer).Encode(d)
}

func (d *Desc) WriteToFile() error {
	file, err := os.Create(d.path)
	if err != nil {
		return err
	}
	defer func() {
		e := file.Close()
		if e != nil {
			err = e
		}
	}()

	return d.WriteYAML(file)
}

func (d *Desc) PackageDir() string {
	descRel := URLVersionToRelPath(d.URL, d.Version)
	return filepath.Join(PackageDescriptionDir, descRel)
}

func (d *Desc) WriteInDir(outDir string) (string, error) {
	p := filepath.Join(outDir, d.PackageDir())
	err := os.MkdirAll(p, 0775)
	if err != nil {
		return "", err
	}
	descPath := filepath.Join(p, DescriptionFileName)
	d.path = descPath
	err = d.WriteToFile()
	if err != nil {
		return "", err
	}
	return descPath, nil
}

// IDCompare compares this and the other description with respect to their ids.
// The ID of a description is its URL combined with the version.
// Invalid versions are considered equal to themselves and less than valid ones.
func (d *Desc) IDCompare(other *Desc) int {
	if d.URL == other.URL {
		a, errA := version.NewVersion(d.Version)
		b, errB := version.NewVersion(other.Version)
		if errA != nil && errB != nil {
			return 0
		}
		if errA != nil {
			return -1
		}
		if errB != nil {
			return 1
		}
		return a.Compare(b)
	}
	return strings.Compare(d.URL, other.URL)
}

func mapSpecDepsToDescDeps(specDeps DependencyMap) []descPackage {
	result := []descPackage{}
	for _, pkg := range specDeps {
		descPkg := descPackage{
			URL:     pkg.URL,
			Version: pkg.Version,
		}
		if pkg.Path != "" && pkg.URL == "" {
			descPkg.URL = "<local path>"
		}
		result = append(result, descPkg)
	}
	sort.Slice(result, func(i int, j int) bool {
		a := result[i]
		b := result[j]
		if a.URL == b.URL {
			return a.Version < b.Version
		}
		return a.URL < b.URL
	})
	return result
}

func scrapeDescriptionAt(path string, allowsLocalDeps AllowLocalDepsFlag, ui UI, verbose func(string, ...interface{})) (*Desc, error) {

	isDir, err := isDirectory(path)
	if err != nil {
		return nil, err
	}
	if !isDir {
		return nil, ui.ReportError("Path '%s' is not a directory", path)
	}

	specPath := filepath.Join(path, DefaultSpecName)
	stat, err := os.Stat(specPath)
	if os.IsNotExist(err) {
		return nil, ui.ReportError("Missing '%s' file in '%s'", DefaultSpecName, path)
	} else if err != nil {
		return nil, err
	} else if stat.IsDir() {
		return nil, ui.ReportError("Not a regular file: '%s'", specPath)
	}

	spec, err := ReadSpec(specPath, ui)
	if err != nil {
		return nil, err
	}

	if allowsLocalDeps != AllowLocalDeps {
		for _, dep := range spec.Deps {
			if dep.Path != "" {
				if allowsLocalDeps == ReportLocalDeps {
					ui.ReportWarning("Dependency to local path: '%s'", dep.Path)
				} else {
					return nil, ui.ReportError("Dependency to local path: '%s'", dep.Path)
				}
			}
		}
	}
	desc := NewDesc(spec.Name,
		spec.Description,
		"<Not scraped for local paths>",
		"<Not scraped for local paths>",
		spec.Environment.SDK,
		spec.License,
		"<Not scraped for local paths>",
		mapSpecDepsToDescDeps(spec.Deps),
	)

	// Packages must have a 'src' directory.
	srcPath := filepath.Join(path, "src")
	srcStat, err := os.Stat(srcPath)
	if os.IsNotExist(err) {
		return nil, ui.ReportError("Missing 'src' directory in '%s'", path)
	} else if err != nil {
		return nil, err
	} else if !srcStat.IsDir() {
		return nil, ui.ReportError("Not a directory: '%s'", srcPath)
	}

	if desc.Name == "" || desc.Description == "" {
		ui.ReportWarning("Automatic name/description extraction from README has been removed.")

		var returnError error
		if desc.Name == "" && desc.Description == "" {
			returnError = ui.ReportError("Missing name and description")
		} else if desc.Name == "" {
			returnError = ui.ReportError("Missing name")
		} else if desc.Description == "" {
			returnError = ui.ReportError("Missing description")
		}
		if returnError != nil {
			return nil, returnError
		}
	}

	if desc.License != "" {
		if !validateLicenseID(desc.License) {
			ui.ReportWarning("Unknown SDIX license-ID: '%s'", desc.License)
		}
	} else {
		licensePath := filepath.Join(path, "LICENSE")
		licenseStat, err := os.Stat(licensePath)
		if os.IsNotExist(err) {
			verbose("No 'LICENSE' file found")
			ui.ReportWarning("Missing license")
		} else if err != nil {
			return nil, err
		} else if licenseStat.IsDir() {
			verbose("'LICENSE' path is a directory")
			ui.ReportWarning("Missing license")
		} else {
			content, err := ioutil.ReadFile(licensePath)
			if err != nil {
				return nil, err
			}
			licenseID := guessLicense(content)
			if licenseID == "" {
				ui.ReportWarning("Unknown license in 'LICENSE' file")
			} else {
				verbose("Using license '%s' from 'LICENSE' file", licenseID)
				desc.License = licenseID
			}
		}
	}

	return desc, nil

}

func ScrapeDescriptionGit(ctx context.Context, url string, v string, allowsLocalDeps AllowLocalDepsFlag, isVerbose bool, ui UI) (*Desc, error) {
	verbose := func(msg string, args ...interface{}) {
		if isVerbose {
			ui.ReportInfo(msg, args...)
		}
	}

	originalVersion := v
	v = strings.TrimPrefix(v, "v")
	_, err := version.NewVersion(v)
	if err != nil {
		return nil, ui.ReportError("Invalid version: '%s'", originalVersion)
	}

	tmpDir, err := ioutil.TempDir("", "tpkg-scrape-git")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// The `DownloadGit` function downloads adjacent to the target directory.
	// We thus add another segment to the directory path.
	dir := filepath.Join(tmpDir, "pkg")

	httpURL := url
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		url = url[strings.Index(url, "/")+2:]
	} else {
		httpURL = "https://" + url
	}
	if strings.HasSuffix(url, ".git") {
		url = url[:len(url)-4]
	}

	verbose("Cloning '%s' into '%s'", httpURL, dir)

	downloadedHash, err := DownloadGit(ctx, DownloadGitOptions{
		Directory: dir,
		URL:       url,
		Version:   v,
		Hash:      "",
		UI:        ui,
	})
	if err != nil {
		return nil, err
	}
	desc, err := scrapeDescriptionAt(dir, allowsLocalDeps, ui, verbose)
	if err != nil {
		return nil, err
	}
	desc.URL = url
	desc.Version = v
	desc.Hash = downloadedHash
	return desc, nil
}

func ScrapeDescriptionAt(path string, allowsLocalDeps AllowLocalDepsFlag, isVerbose bool, ui UI) (*Desc, error) {
	verbose := func(msg string, args ...interface{}) {
		if isVerbose {
			ui.ReportInfo(msg, args...)
		}
	}
	return scrapeDescriptionAt(path, allowsLocalDeps, ui, verbose)
}
