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

	"github.com/toitlang/tpkg/pkg/compiler"
)

// Cache handles all package-Cache related functionality.
// It keeps track of where the caches are, and how to compute paths for packages.
type Cache struct {
	ui      UI
	options *cacheOptions
}

type cacheOptions struct {
	// If set, the location where new packages should be installed.
	// Otherwise uses `ProjectPackagesPath` (normally `.packages`) in the project root.
	installPkgPath *string
	// The locations where packages can be found.
	// if InstallPkgPath is set, it will be added as the first path in pkgCachePaths.
	pkgCachePaths []string
	// The locations where git registries can be found.
	// The first path is used to install new git registries.
	registryCachePaths []string
}

func (o *cacheOptions) apply(options ...CacheOption) {
	for _, option := range options {
		option.applyCacheOption(o)
	}
	if o.installPkgPath != nil {
		o.pkgCachePaths = append([]string{*o.installPkgPath}, o.pkgCachePaths...)
	}
}

// CacheOption defines the optional parameters for NewCache.
type CacheOption interface {
	applyCacheOption(*cacheOptions)
}

// WithPkgCachePath sets the locations where packages can be found.
func WithPkgCachePath(paths ...string) CacheOption {
	return pkgCachePaths(paths)
}

type pkgCachePaths []string

func (p pkgCachePaths) applyCacheOption(o *cacheOptions) {
	o.pkgCachePaths = append(o.pkgCachePaths, p...)
}

// WithRegistryCachePath sets the locations where git registries can be found.
func WithRegistryCachePath(paths ...string) CacheOption {
	return registryCachePaths(paths)
}

type registryCachePaths []string

func (p registryCachePaths) applyCacheOption(o *cacheOptions) {
	o.registryCachePaths = append(o.registryCachePaths, p...)
}

// WithPkgInstallPath sets the locations where git registries can be found.
func WithPkgInstallPath(path string) CacheOption {
	return pkgInstallPath(path)
}

type pkgInstallPath string

func (p pkgInstallPath) applyCacheOption(o *cacheOptions) {
	path := string(p)
	o.installPkgPath = &path
}

// NewCache creates a new package cache and uses the registryPath as the locations where git registries
// will be installed can be found.
func NewCache(registryPath string, ui UI, options ...CacheOption) Cache {
	option := &cacheOptions{
		registryCachePaths: []string{
			registryPath,
		},
	}
	option.apply(options...)

	return Cache{
		options: option,
		ui:      ui,
	}
}

func (c Cache) find(p string, paths []string) (string, error) {
	for _, cachePath := range paths {
		cachePath := filepath.Join(cachePath, p)
		info, err := os.Stat(cachePath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", c.ui.ReportError("Path %s exists but is not a directory", p)
		}
		return cachePath, nil
	}
	return "", nil
}

// FindPkg searches for the path of 'url'-'version' in the cache.
// If it's not found returns "".
func (c Cache) FindPkg(rootPath string, url string, version string) (string, error) {
	packageRel := URLVersionToRelPath(url, version)
	fullProjectPackagesPath := filepath.Join(rootPath, ProjectPackagesPath)
	return c.find(packageRel, append([]string{fullProjectPackagesPath}, c.options.pkgCachePaths...))
}

// FindRegistry searches for the path of the registry with the given url in the cache.
// If it's not found returns "".
func (c Cache) FindRegistry(url string) (string, error) {
	registryRel := urlToRelPath(url)
	return c.find(registryRel, c.options.registryCachePaths)
}

// Returns the path to the specification of the package url-version.
// If the package is not in the cache returns "".
func (c Cache) SpecPathFor(projectRootPath string, url string, version string) (string, error) {
	pkgPath, err := c.FindPkg(projectRootPath, url, version)
	if err != nil {
		return "", err
	}
	if pkgPath == "" {
		return "", nil
	}
	specPath := filepath.Join(pkgPath, DefaultSpecName)
	ok, err := isFile(specPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("missing spec file for package '%s/%s'", url, version)
	}
	return specPath, nil
}

// PreferredPkgPath returns the preferred path for the package url-version.
func (c Cache) PreferredPkgPath(projectRootPath string, url string, version string) string {
	packageRel := URLVersionToRelPath(url, version)
	return filepath.Join(c.PkgInstallPath(projectRootPath), packageRel)
}

func (c Cache) PkgInstallPath(projectRootPath string) string {
	if c.options.installPkgPath != nil {
		return *c.options.installPkgPath
	}
	return filepath.Join(projectRootPath, ProjectPackagesPath)
}

// PreferredRegistryPath returns the preferred path for the given registry url.
func (c Cache) PreferredRegistryPath(url string) string {
	// The first cache path is the preferred location.
	escapedURL := string(compiler.ToURIPath(url))
	return filepath.Join(c.options.registryCachePaths[0], filepath.FromSlash(escapedURL))
}

const readmeContent string = `# Package Cache Directory

This directory contains Toit packages that have been downloaded by
the Toit package management system.

Generally, the package manager is able to download these packages again. It
is thus safe to remove the content of this directory.
`

// CreatePackagesCacheDir creates the package cache dir.
// If the directory doesn't exist yet, creates it, and writes a README
// explaining what the directory is for, and what is allowed to be done.
func (c Cache) CreatePackagesCacheDir(projectRootPath string, ui UI) error {
	packagesCacheDir := c.PkgInstallPath(projectRootPath)
	stat, err := os.Stat(packagesCacheDir)
	if err == nil && !stat.IsDir() {
		return ui.ReportError("Package cache path already exists but is not a directory: '%s'", packagesCacheDir)
	}
	if !os.IsNotExist(err) {
		return err
	}
	err = os.Mkdir(packagesCacheDir, 0755)
	if err != nil {
		return err
	}
	readmePath := filepath.Join(packagesCacheDir, "README.md")
	err = ioutil.WriteFile(readmePath, []byte(readmeContent), 0755)
	return err
}
