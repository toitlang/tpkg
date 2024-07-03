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
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

// pkgPathForDir returns the pkg-init file in the given directory.
// The given dir must not be empty.
func pkgPathForDir(dir string) string {
	if dir == "" {
		log.Fatal("Directory must not be empty")
	}
	return filepath.Join(dir, DefaultSpecName)
}

// lockPathForDir returns the lock file in the given directory.
// The given dir must not be empty.
func lockPathForDir(dir string) string {
	if dir == "" {
		log.Fatal("Directory must not be empty")
	}

	return filepath.Join(dir, DefaultLockFileName)
}

// InitDirectory initializes the project root as the root for a package or application.
// If no root is given, initializes the current directory instead.
func InitDirectory(projectRoot string, name string, description string, ui UI) error {
	if name == "" {
		return ui.ReportError("Name must be provided")
	}

	dir := projectRoot
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}
	pkgPath := pkgPathForDir(dir)
	lockPath := lockPathForDir(dir)

	pkgExists, err := isFile(pkgPath)
	if err != nil {
		return err
	}
	lockExists, err := isFile(lockPath)
	if err != nil {
		return err
	}

	if pkgExists || lockExists {
		ui.ReportInfo("Directory '%s' already initialized", dir)
		return nil
	}

	spec := Spec{
		path:        pkgPath,
		Name:        name,
		Description: description,
	}
	err = spec.WriteToFile()
	if err != nil {
		return err
	}
	return ioutil.WriteFile(lockPath, []byte("# Toit Lock File.\n"), 0644)
}
