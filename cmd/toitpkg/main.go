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

package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/toitlang/tpkg/commands"
	"github.com/toitlang/tpkg/config"
	"github.com/toitlang/tpkg/config/store"
	"github.com/toitlang/tpkg/pkg/tracking"
)

var (
	rootCmd = &cobra.Command{
		Use:              "toitpkg",
		Short:            "Run pkg commands",
		TraverseChildren: true,
	}
)

func getTrimmedEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func main() {
	cfgFile := getTrimmedEnv("TOIT_CONFIG_FILE")
	cacheDir := getTrimmedEnv("TOIT_CACHE_DIR")
	noDefaultRegistry := getTrimmedEnv("TOIT_NO_DEFAULT_REGISTRY")
	shouldPrintTracking := getTrimmedEnv("TOIT_SHOULD_PRINT_TRACKING")
	sdkVersion := getTrimmedEnv("TOIT_SDK_VERSION")
	noAutosync := getTrimmedEnv("TOIT_NO_AUTO_SYNC")

	track := func(ctx context.Context, te *tracking.Event) error {
		if shouldPrintTracking != "" {
			tmpl := template.Must(template.New("tracking").Parse(`Name: {{.Name}}
{{if .Properties }}Properties:{{ range $field, $value := .Properties }}
  {{$field}}: {{$value}}{{end}}{{end}}
`))
			out := bytes.Buffer{}
			if err := tmpl.Execute(&out, te); err != nil {
				log.Fatal("Unexpected error while using template. %w", err)
			}
			fmt.Print(out.String())
		}
		return nil
	}

	configStore := store.NewViper(cacheDir, sdkVersion, noAutosync != "", noDefaultRegistry != "")
	cobra.OnInitialize(func() {
		if cfgFile == "" {
			cfgFile, _ = config.UserConfigFile()
		}
		configStore.Init(cfgFile)
	})

	pkgCmd, err := commands.Pkg(commands.DefaultRunWrapper, track, configStore, nil)
	if err != nil {
		e, ok := err.(commands.WithSilent)
		if !ok {
			fmt.Fprintln(os.Stderr, e)
		}
	}
	rootCmd.AddCommand(pkgCmd)
	rootCmd.Execute()
}
