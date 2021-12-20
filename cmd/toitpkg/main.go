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

	"github.com/spf13/cobra"
	"github.com/toitlang/tpkg/commands"
	"github.com/toitlang/tpkg/config"
	"github.com/toitlang/tpkg/config/store"
	"github.com/toitlang/tpkg/pkg/tracking"
)

var (
	// Used for flag.
	cfgFile             string
	cacheDir            string
	noDefaultRegistry   bool
	shouldPrintTracking bool
	sdkVersion          string
	noAutosync          bool

	rootCmd = &cobra.Command{
		Use:              "toitpkg",
		Short:            "Run pkg commands",
		TraverseChildren: true,
	}
)

func main() {
	// We use the configurations in the viperConf below.
	// If we didn't want to use the globals we could also switch to
	// a PreRun function.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	rootCmd.Flags().MarkHidden("config")
	rootCmd.PersistentFlags().StringVar(&cacheDir, "cache", "", "cache dir")
	rootCmd.Flags().MarkHidden("cache")
	rootCmd.PersistentFlags().BoolVar(&noDefaultRegistry, "no-default-registry", false, "Don't use default registry if none exists")
	rootCmd.Flags().MarkHidden("no-default-registry")
	rootCmd.PersistentFlags().BoolVar(&noAutosync, "no-autosync", false, "Don't automatically sync")
	rootCmd.Flags().MarkHidden("no-autosync")
	rootCmd.PersistentFlags().BoolVar(&shouldPrintTracking, "track", false, "Print tracking information")
	rootCmd.Flags().MarkHidden("track")

	rootCmd.PersistentFlags().StringVar(&sdkVersion, "sdk-version", "", "The SDK version")

	track := func(ctx context.Context, te *tracking.Event) error {
		if shouldPrintTracking {
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

	configStore := store.NewViper(cacheDir, sdkVersion, noAutosync, noDefaultRegistry)
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
