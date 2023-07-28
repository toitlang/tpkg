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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DescIDCompare(t *testing.T) {
	t.Run("Id Compare", func(t *testing.T) {
		// Name ("z", "x", "a" or "b") doesn't matter in IDCompare.
		a1 := NewDesc("z", "", "a", "1.0.0", "", "MIT", "", []descPackage{})
		a2 := NewDesc("x", "", "a", "1.0.2", "", "MIT", "", []descPackage{})
		b := NewDesc("a", "", "b", "0.0.1", "", "MIT", "", []descPackage{})
		bSame := NewDesc("B", "", "b", "0.0.1", "", "MIT", "", []descPackage{})
		assert.Equal(t, 0, a1.IDCompare(a1))
		assert.Equal(t, 0, a2.IDCompare(a2))
		assert.Equal(t, 0, b.IDCompare(b))
		assert.Equal(t, 0, bSame.IDCompare(bSame))
		assert.Equal(t, 0, bSame.IDCompare(b))
		assert.Equal(t, 0, b.IDCompare(bSame))
		assert.Equal(t, -1, a1.IDCompare(a2))
		assert.Equal(t, -1, a1.IDCompare(b))
		assert.Equal(t, -1, a1.IDCompare(bSame))
		assert.Equal(t, -1, a2.IDCompare(b))
		assert.Equal(t, -1, a2.IDCompare(bSame))
		assert.Equal(t, 1, a2.IDCompare(a1))
		assert.Equal(t, 1, b.IDCompare(a1))
		assert.Equal(t, 1, bSame.IDCompare(a1))
		assert.Equal(t, 1, b.IDCompare(a2))
		assert.Equal(t, 1, bSame.IDCompare(a2))
	})
}

func Test_CaretVersion(t *testing.T) {
	t.Parallel()
	t.Run("Parse caret versions", func(t *testing.T) {
		tests := [][]string{
			{"^1.2.3", ">=1.2.3,<2.0.0"},
			{"^0.2.3", ">=0.2.3,<0.3.0"},
			{"^0.0.1", ">=0.0.1,<0.0.2"},
			// The trailing "0"s are not necessary, but make it easier to compare with the
			// actual constraint.
			{"^0.0.1.4", ">=0.0.1.4,<0.0.2.0"},
			{"^0.0.1.4.5", ">=0.0.1.4.5,<0.0.2.0.0"},
			{"^1.2.3-beta", ">=1.2.3-beta,<2.0.0"},
			{"^1.2.3+beta", ">=1.2.3+beta,<2.0.0"},
		}
		for _, test := range tests {
			t.Run(test[0], func(t *testing.T) {
				in := test[0]
				expectedIn := test[1]
				actual, err := parseConstraint(in)
				require.NoError(t, err)
				expected, err := parseConstraint(expectedIn)
				require.NoError(t, err)
				assert.Equal(t, expected.String(), actual.String())
			})
		}
	})
}
