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

import "fmt"

// UI allows this package to interact with the user.
//
// This package will report user-facing errors (like missing packages)
// through this interface.
// The package might report multiple errors.
// If an action wasn't successful (like installing a package), then
// the package reports the error and then returns an AlreadyReportedError.
// This indicates to the caller that the operation failed, but that no
// further information needs to be printed.
type UI interface {
	// ReportError signals an error to the user.
	// The format string is currently compatible with fmt.Printf.
	// Returns ErrAlreadyReported.
	ReportError(format string, a ...interface{}) error

	// ReportError signals a warning to the user.
	// The format string is currently compatible with fmt.Printf.
	ReportWarning(format string, a ...interface{})

	// ReportInfo reports interesting information.
	ReportInfo(format string, a ...interface{})
}

// FmtUI implements a simple version of UI that prints messages using `fmt` primitives.
type fmtUI struct{}

// ReportError reports errors from the tpkg package.
// Returns 'ErrAlreadyReported'
func (ui fmtUI) ReportError(format string, a ...interface{}) error {
	fmt.Printf("Error: "+format+"\n", a...)
	return ErrAlreadyReported
}

// ReportWarning reports warnings from the tpkg package.
func (ui fmtUI) ReportWarning(format string, a ...interface{}) {
	fmt.Printf("Warning: "+format+"\n", a...)
}

func (ui fmtUI) ReportInfo(format string, a ...interface{}) {
	fmt.Printf("Info: "+format+"\n", a...)
}

// nullUI implements a UI that does nothing.
type nullUI struct{}

func (ui nullUI) ReportError(format string, a ...interface{}) error {
	return ErrAlreadyReported
}

func (ui nullUI) ReportWarning(format string, a ...interface{}) {
}

func (ui nullUI) ReportInfo(format string, a ...interface{}) {
}

var (
	// ErrAlreadyReported can be used to signal that an error has
	// been reported, and that no further action needs to be taken.
	// The returned error should be interchangeable. That is, one should
	// be able to call this function multiple times and just return any
	// of the received errors.
	// In case the error gets printed anyway, we have a sensible error message
	// instead of "already reported" or similar.
	ErrAlreadyReported = fmt.Errorf("package management error")

	// FmtUI is simple version of UI that uses 'fmt' to report warnings and errors.
	FmtUI UI = fmtUI{}
)

// IsErrAlreadyReported returns whether 'e' is the ErrAlreadyReported error.
func IsErrAlreadyReported(e error) bool {
	return e == ErrAlreadyReported
}
