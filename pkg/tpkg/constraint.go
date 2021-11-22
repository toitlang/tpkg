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
	"fmt"
	"strings"

	"github.com/hashicorp/go-version"
)

type rangeKind int

const (
	// A segment range means that the constraint accepts any digit for the
	// missing segments.
	// For example, version "1.2" accepts "1.2.3" or "1.2.9".
	segmentRange rangeKind = iota
	// A semver constraint accepts all versions that are semver compatible.
	semverRange
)

func parseInstallConstraint(str string) (version.Constraints, error) {
	return parseConstraintRange(str, segmentRange)
}

func parseConstraint(str string) (version.Constraints, error) {
	if !strings.Contains(str, "^") {
		return version.NewConstraint(str)
	}

	// Toit supports constraints of the form '^version' which is equivalent to
	// '>=version,<nextIncompatibleVersion'
	// For example:
	//    '^1.2.3' is equivalent to '>=1.2.3,<2.0.0' and
	//    '^0.1.2' is equivalent to '>=0.1.2,<0.2.0'
	parts := strings.Split(str, ",")
	var result version.Constraints
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "^") {
			vStr := strings.TrimPrefix(p, "^")
			cs, err := parseConstraintRange(vStr, semverRange)
			if err != nil {
				return nil, err
			}
			result = append(result, cs...)

		} else {
			cs, err := version.NewConstraint(p)
			if err != nil {
				return nil, err
			}
			result = append(result, cs...)
		}
	}
	return result, nil
}

func parseConstraintRange(vStr string, kind rangeKind) (version.Constraints, error) {
	v, err := version.NewVersion(vStr)
	if err != nil {
		return nil, err
	}
	segments := v.Segments()
	upper := ""
	if kind == semverRange {
		reset := false
		for i, segment := range segments {
			if reset {
				segments[i] = 0
			} else if segment != 0 {
				segments[i] = segment + 1
				reset = true
			}
		}
		strs := make([]string, len(segments))
		for i, segment := range segments {
			strs[i] = fmt.Sprint(segment)
		}
		upper = strings.Join(strs, ".")
	} else {
		dots := strings.Count(vStr, ".")
		if dots == 0 {
			upper = fmt.Sprintf("%d.0.0", segments[0]+1)
		} else if dots == 1 {
			upper = fmt.Sprintf("%d.%d.0", segments[0], segments[1]+1)
		} else {
			// Just use the version that was given as constraint.
			return version.NewConstraint(vStr)
		}
	}
	expandedConstraint := ">=" + vStr + ",<" + upper
	return version.NewConstraint(expandedConstraint)
}
