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

package compiler

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

/*
A compiler path is a path that is recognized by the compiler.
Fundamentally it requires:
- absolute paths must start with '/'
- the segment separator must be a '/'.

These functions must be kept in sync with the one from toitlsp.
*/

type Path string

func ToPath(path string) Path {
	return toCompilerPath(path, runtime.GOOS == "windows")
}

func toCompilerPath(path string, windows bool) Path {
	if !windows {
		return Path(path)
	}
	if filepath.IsAbs(path) {
		path = "/" + path
	}
	return Path(filepath.ToSlash(path))
}

func (path Path) FilePath() string {
	return fromCompilerPath(path, runtime.GOOS == "windows")
}

func fromCompilerPath(path Path, onWindows bool) string {
	p := string(path)
	if !onWindows {
		return p
	}

	p = strings.TrimPrefix(p, "/")
	return filepath.FromSlash(p)
}

// URIPath is a url suitable as a '/' separated path.
// That is, the URL can be used as a path once the '/'s are translated to OS specific
// path-segment separators. Most importantly, such a URL does not contain any `:` or
// any other characters that are not allowed in paths.
// The URIPath is used in lock files where the compiler uses it to find the
// dependent packages.
// A URIPath can always be converted back to the original URL.
// A URIPath doesn't need to be a valid URL (although it typically is and resembles one).
type URIPath string

// ToURIPath takes a URL and converts it to an URIPath.
func ToURIPath(u string) URIPath {
	// Split the URL at '/'.
	segments := strings.Split(u, "/")
	// Escape each segment.
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
		// If the segment is one of the dangerous filenames (on Windows), then escape it.
		all_dangerous := []string{
			"",
			"CON", "PRN", "AUX", "NUL",
			"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		}
		for _, dangerous := range all_dangerous {
			if strings.ToUpper(segments[i]) == dangerous {
				// Escape the segment by adding a '%' at the end.
				// This ensures that the segment is a valid file name.
				// It's not a valid URL anymore, as the '%' is not a correct escape. However,
				// this also guarantees that we don't accidentally clash with any other
				// segment.
				segments[i] = segments[i] + "%"
			}
		}
		if strings.HasSuffix(segments[i], ".") {
			segments[i] = segments[i] + "%"
		}
	}
	return URIPath(strings.Join(segments, "/"))
}

// URL undoes the escaping done in ToEscapedURLPath.
func (up URIPath) URL() string {
	// Split the URL at '/'.
	segments := strings.Split(string(up), "/")
	// Unescape each segment.
	for i, segment := range segments {
		if strings.HasSuffix(segments[i], "%") {
			segment = segment[:len(segment)-1]
		}
		segments[i], _ = url.PathUnescape(segment)
	}
	return strings.Join(segments, "/")
}

func (up URIPath) FilePath() string {
	return filepath.FromSlash(string(up))
}

// FilePathToURIPath encodes a file path as a URIPath.
// For this operation it just converts the path to slash without
// any escaping.
func FilePathToURIPath(p string) URIPath {
	return ToURIPath(filepath.ToSlash(p))
}
