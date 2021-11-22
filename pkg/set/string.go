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

package set

type String map[string]struct{}

func NewString(strs ...string) String {
	res := String{}
	for _, s := range strs {
		res[s] = struct{}{}
	}
	return res
}

func (s *String) Add(strs ...string) {
	if *s == nil {
		*s = String{}
	}

	for _, str := range strs {
		(*s)[str] = struct{}{}
	}
}

func (s String) Remove(strs ...string) {
	if s == nil {
		return
	}

	for _, str := range strs {
		delete(s, str)
	}
}

func (s String) Contains(str string) bool {
	if s == nil {
		return false
	}

	_, exists := s[str]
	return exists
}

func (s String) Values() []string {
	var res []string
	if s == nil {
		return res
	}

	for k := range s {
		res = append(res, k)
	}
	return res
}
