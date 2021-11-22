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
)

func UserConfigPath() (string, error) {
	if path, ok := os.LookupEnv(UserConfigDirEnv); ok {
		return path, nil
	}

	homedir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homedir, ".config", "toit"), nil
}

// UserConfigFile returns the config file in the user directory.
func UserConfigFile() (string, bool) {
	if homedir, err := EnsureDirectory(UserConfigPath()); err == nil {
		return filepath.Join(homedir, "config.yaml"), true
	}
	return "", false
}
