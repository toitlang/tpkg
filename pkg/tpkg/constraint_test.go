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
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseConstraintRange(t *testing.T) {
	tests := [][]string{
		{"0", ">=0,<1.0.0"},
		{"1", ">=1,<2.0.0"},
		{"0.5", ">=0.5,<0.6.0"},
		{"1.5", ">=1.5,<1.6.0"},
		{"0.5.3", "0.5.3"},
		{"1.5.3", "1.5.3"},
		{"1.5.3-alpha", "1.5.3-alpha"},
		{"0.0.1.4.5", "0.0.1.4.5"},
	}
	for _, test := range tests {
		t.Run(test[0], func(t *testing.T) {
			in := test[0]
			expectedIn := test[1]
			actual, err := parseInstallConstraint(in)
			require.NoError(t, err)
			expected, err := version.NewConstraint(expectedIn)
			require.NoError(t, err)
			assert.Equal(t, expected.String(), actual.String())
		})
	}
}
