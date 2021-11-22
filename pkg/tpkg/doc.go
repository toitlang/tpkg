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

// Package tpkg provides functions to manage Toit packages.
//
// Key concepts:
// * Package: a reusable component.
// * Package description (Desc): description of a package that is distributed
//   independently of a package. The description serves to find a package and all of its
//   dependencies.
//   Package resolution (deciding which versions...) is done with descriptions alone,
//   without downloading any sources.
// * Package specification (Spec): a specification shipped with a package that contains
//   relevant information.
//   Fundamentally, a specification is not necessary, as only a description is necessary
//   to make packages usable. In practice, we use specifications to automatically build
//   descriptions (together with additional information that can often be extracted
//   automatically).
//   For example, dependencies should be written into a specification file.
//   We also use specification files to recognize folders as packages. If such a file
//   exists, we know that the folder should be treated as package.
// * Registry: a place where descriptions of available packages can be found.
//   There can be multiple registries, with different properties. For example,
//   some registries can be public, while others can be private.
// * Lock file: the result of resolving all versions of an application. Contains
//   an easy way for the compiler to find concrete sources for each package the application
//   transitively uses.
//   One can think of the entries in the lock file as a mapping from package-name (used as
//   dependency) to an absolute path. However, since lock files are often shared (and
//   checked in), the mapping is a bit more complicated and usually doesn't have absolute
//   paths.
package tpkg
