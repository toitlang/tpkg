// Copyright (C) 2023 Toitware ApS.
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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_toPathURI(t *testing.T) {
	tests := [][]string{
		{"github.com/foo/bar", "github.com/foo/bar"},
		{"github.com/foo/bar+gee", "github.com/foo/bar%2Bgee"},
		{"github.com/foo/bar_gee", "github.com/foo/bar_gee"},
		{"github.com/foo/bar-gee", "github.com/foo/bar-gee"},
		{"github.com/foo%/xx", "github.com/foo%25/xx"},
		{"c:\\github\\foo", "c%3A/github/foo"},
		{"", "%"},
		{"con/prn", "con%/prn%"},
		{"CON/PRN", "CON%/PRN%"},
		{"foo/bar/gee./toto", "foo/bar/gee.%/toto"},
	}
	for _, test := range tests {
		t.Run(test[0], func(t *testing.T) {
			in := test[0]
			expected := test[1]
			actual := ToURIPath(in)
			assert.Equal(t, expected, string(actual))
			slashOnly := strings.ReplaceAll(in, "\\", "/")
			assert.Equal(t, slashOnly, actual.URL())
		})
	}
}
