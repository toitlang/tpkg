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

package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	packageCacheSubDir  = "tpkg"
	registryCacheSubDir = "tpkg-registries"
	// ToitPackageCachePathsEnv contains the paths where the compiler is looking for cached packages.
	// This constant must be kept in sync with the one in the compiler.
	ToitPackageCachePathsEnv = "TOIT_PACKAGE_CACHE_PATHS"
	// ToitRegistryCachePathsEnv contains the paths where the compiler is looking for cached registries.
	ToitRegistryCachePathsEnv = "TOIT_REGISTRY_CACHE_PATHS"
	// ToitRegistryInstallPathEnv contains the paths where tpkg will install packages for the project.
	ToitRegistryInstallPathEnv = "TOIT_PACKAGE_INSTALL_PATH"
	// UserConfigDirEnv if set, will be the directory the user config will be loaded from.
	UserConfigDirEnv = "TOIT_USER_CONFIG_DIR"
)

func EnsureDirectory(dir string, err error) (string, error) {
	if err != nil {
		return dir, err
	}
	return dir, os.MkdirAll(dir, 0755)
}

func CachePath() (string, error) {
	homedir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homedir, ".cache", "toit"), nil
}

func cachePathFor(subDir string) (string, error) {
	cachePath, err := CachePath()
	if err != nil {
		return "", err
	}

	return filepath.Join(cachePath, subDir), nil
}

func computeCachePaths(envName string, defaultSubdir string) ([]string, error) {
	variable, exists := os.LookupEnv(envName)
	if exists {
		parts := strings.Split(variable, ":")
		if len(parts) != 0 {
			return parts, nil
		}
	}
	defaultPath, err := cachePathFor(defaultSubdir)
	if err != nil {
		return nil, err
	}
	return []string{
		defaultPath,
	}, nil
}

// PackageCachePaths returns the paths where the compiler is looking for cached packages.
func PackageCachePaths() ([]string, error) {
	return computeCachePaths(ToitPackageCachePathsEnv, packageCacheSubDir)
}

// RegistryCachePaths returns the paths where the compiler is looking for cached registries.
func RegistryCachePaths() ([]string, error) {
	return computeCachePaths(ToitRegistryCachePathsEnv, registryCacheSubDir)
}

func PackageInstallPath() (string, bool) {
	return os.LookupEnv(ToitRegistryInstallPathEnv)
}
