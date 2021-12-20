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

package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func FirstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func ErrorMessage(err error) string {
	return status.Convert(err).Message()
}

func IsAlreadyExistsError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}

type WithExitCode interface {
	ExitCode() int
}

type WithSilent interface {
	Silent() bool
}

func DefaultRunWrapper(f CobraErrorCommand) CobraCommand {
	return func(cmd *cobra.Command, args []string) {
		err := f(cmd, args)
		if err != nil {
			_, ok := err.(WithSilent)
			if !ok {
				fmt.Fprintf(os.Stderr, "Unhandled error: %v\n", err)
			}
			e, ok := err.(WithExitCode)
			if ok {
				os.Exit(e.ExitCode())
			}
			os.Exit(1)
		}
	}
}
