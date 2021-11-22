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

const (
	// ProjectPackagesPath provides the path, relative to the project's root,
	// into which packages should be downloaded.
	ProjectPackagesPath = ".packages"

	DefaultSpecName     = "package.yaml"
	DefaultLockFileName = "package.lock"

	// The directory inside registries, where descriptions should be stored.
	PackageDescriptionDir = "packages"

	// The default filename for description files.
	DescriptionFileName = "desc.yaml"
)
