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

func buildTestPathRegistries() Registries {
	entries1 := []*Desc{
		{
			Name:    "desc1-1",
			URL:     "desc1URL",
			Version: "1.0.0",
		},
		{
			Name:    "desc1-2",
			URL:     "desc1URL",
			Version: "2.0.0",
		},
	}
	entries2 := []*Desc{
		{
			Name:    "different desc1-1",
			URL:     "desc1URL",
			Version: "1.0.0",
		},
		{
			Name:    "desc2-1",
			URL:     "desc2URL",
			Version: "1.0.0",
		},
	}
	return Registries{
		&pathRegistry{
			path:    "not important",
			entries: entries1,
		},
		&pathRegistry{
			path:    "not important2",
			entries: entries2,
		},
	}
}

func Test_DescForURL(t *testing.T) {
	t.Run("SearchURL", func(t *testing.T) {
		m := Manager{
			registries: buildTestPathRegistries(),
			ui:         FmtUI,
		}

		found, err := m.registries.SearchURLVersion("desc1URL", "1.0.0")
		require.NoError(t, err)
		assert.Equal(t, 2, len(found))
		assert.Equal(t, "desc1-1", found[0].Desc.Name)
		assert.Equal(t, "different desc1-1", found[1].Desc.Name)

		found, err = m.registries.SearchURLVersion("desc1URL", "2.0.0")
		require.NoError(t, err)
		assert.Equal(t, 1, len(found))
		assert.Equal(t, "desc1-2", found[0].Desc.Name)

		found, err = m.registries.SearchURLVersion("desc2URL", "1.0.0")
		require.NoError(t, err)
		assert.Equal(t, 1, len(found))
		assert.Equal(t, "desc2-1", found[0].Desc.Name)

		found, err = m.registries.SearchURLVersion("desc2URL", "3.0.0")
		require.NoError(t, err)
		assert.Equal(t, 0, len(found))
	})
}
