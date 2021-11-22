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
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/toitlang/tpkg/pkg/git"
)

const TestGitPathHost = "path.toit.local"

func makeContainedReadOnly(dir string, ui UI) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == dir {
			return nil
		}
		if info.IsDir() {
			// Don't change the permissions of directories.
			return nil
		}
		writeBits := uint32(0222)
		info.Mode()
		err = os.Chmod(path, os.FileMode(uint32(info.Mode()) & ^writeBits))
		if err != nil {
			ui.ReportWarning("Error while setting '%s' to read-only: %v", path, err)
		}
		return nil
	})
}

// decomposePkgURL takes a package URL and splits into repository-URL and path.
// The URL can be used to check out the repository, and the path then points to
// the package in the repository.
// For example `github.com/toitware/test-pkg.git/bar/gee` is decomposed into
// `github.com/toitware/test-pkg` and `bar/gee`
func decomposePkgURL(url string) (string, string) {
	if lastIndex := strings.LastIndex(url, ".git/"); lastIndex >= 0 {
		path := url[lastIndex+len(".git/"):]
		url = url[:lastIndex]
		return url, path
	}
	return url, ""
}

// DownloadGit downloads a package, defined by [url] and [version] into the given
// [dir].
// If the [dir] exists it will first be removed to erase old data.
// This function might create an adjacent directory first. For example, if the target
// is `download/here`, then this function might first create a `download/tmp` directory.
// Returns the checked-out hash.

type DownloadGitOptions struct {
	Directory  string
	URL        string
	Version    string
	Hash       string
	UI         UI
	NoReadOnly bool
}

func DownloadGit(ctx context.Context, o DownloadGitOptions) (string, error) {
	_, err := os.Stat(o.Directory)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	} else if err != nil {
		err = os.RemoveAll(o.Directory)
		if err != nil {
			return "", o.UI.ReportError("Failed to remove old package directory '%s': %v", o.Directory, err)
		}
	}

	cloneURL := ""
	path := ""
	tag := o.Version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	checkoutDir := o.Directory

	// If the url's host is our test-host, then we know that the URL's path
	// should be used as file path.
	// Otherwise we assume it's a https-URL.
	if strings.HasPrefix(o.URL, TestGitPathHost+"/") {
		cloneURL = filepath.FromSlash(strings.TrimPrefix(o.URL, TestGitPathHost+"/"))
		path = o.URL
	} else {
		cloneURL, path = decomposePkgURL(o.URL)

		if path != "" {
			lastSegment := path[strings.LastIndex(path, "/")+1:] // Note that this also works if there isn't any '/'.
			tag = lastSegment + "-v" + o.Version
			// Download into a directory adjacent to the final target.
			baseDir := filepath.Dir(o.Directory)
			err = os.MkdirAll(baseDir, 0755)
			if err != nil {
				return "", err
			}
			// The checkout directory must be on the same drive as the final target, as we are using a
			// rename-command to move the nested package to its final position.
			checkoutDir, err = ioutil.TempDir(baseDir, "partial-toit-checkout")
			if err != nil {
				return "", o.UI.ReportError("Failed to create temporary directory to download '%s - %s': %v", o.URL, o.Version, err)
			}
			defer os.RemoveAll(checkoutDir)
		}
	}

	err = os.MkdirAll(checkoutDir, 0755)
	if err != nil {
		return "", o.UI.ReportError("Failed to create download directory '%s': %v", checkoutDir, err)
	}
	successfullyDownloaded := false
	defer func() {
		if !successfullyDownloaded {
			// Try not to leave partially downloaded packages around.
			os.RemoveAll(checkoutDir)
		}
	}()

	downloadedHash, err := git.Clone(ctx, checkoutDir, git.CloneOptions{
		URL:          cloneURL,
		SingleBranch: true,
		Depth:        1,
		Tag:          tag,
		Hash:         o.Hash,
	})

	if err != nil {
		return "", o.UI.ReportError("Error while cloning '%s' with tag '%s': %v", o.URL, tag, err)
	}

	if checkoutDir == o.Directory {
		if !o.NoReadOnly {
			makeContainedReadOnly(o.Directory, o.UI)
		}
		successfullyDownloaded = true
		return downloadedHash, nil
	}
	// We still need to move the package into its correct location.

	nestedPath := filepath.Join(checkoutDir, filepath.FromSlash(path))
	stat, err := os.Stat(nestedPath)
	if os.IsNotExist(err) {
		return "", o.UI.ReportError("Repository '%s' does not have path '%s'", o.URL, path)
	} else if err != nil {
		return "", err
	} else if !stat.IsDir() {
		return "", o.UI.ReportError("Path '%s' in repository '%s' is not a directory", path, o.URL)
	}

	// Renaming only works when the two locations are on the same drive. This is why we didn't
	// check out into a /tmp directory, but checked out in an adjacent directory instead.
	err = os.Rename(nestedPath, o.Directory)
	if err != nil {
		return "", o.UI.ReportError("Failed to move nested package '%s' to its location '%s'", nestedPath, o.Directory)
	}

	if !o.NoReadOnly {
		makeContainedReadOnly(o.Directory, o.UI)
	}
	successfullyDownloaded = true
	return downloadedHash, nil
}
