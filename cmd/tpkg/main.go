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
	"path/filepath"

	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/toitlang/tpkg/commands"
	"github.com/toitlang/tpkg/config"
	"github.com/toitlang/tpkg/pkg/tpkg"
	"github.com/toitlang/tpkg/pkg/tracking"
)

type withExitCode interface {
	ExitCode() int
}

type withSilent interface {
	Silent() bool
}

var (
	// Used for flag.
	cfgFile             string
	cacheDir            string
	noDefaultRegistry   bool
	shouldPrintTracking bool
	sdkVersion          string
	noAutosync          bool

	rootCmd = &cobra.Command{
		Use:              "tpkg",
		Short:            "Run pkg commands",
		TraverseChildren: true,
	}
)

func main() {
	cobra.OnInitialize(initConfig)
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

	runWrapper := func(f commands.CobraErrorCommand) commands.CobraCommand {
		return func(cmd *cobra.Command, args []string) {
			err := f(cmd, args)
			if err != nil {
				_, ok := err.(withSilent)
				if !ok {
					fmt.Fprintf(os.Stderr, "Unhandled error: %v\n", err)
				}
				e, ok := err.(withExitCode)
				if ok {
					os.Exit(e.ExitCode())
				}
				os.Exit(1)
			}
		}
	}

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

	pkgCmd, err := commands.Pkg(runWrapper, track, &viperConfigStore{}, nil)
	if err != nil {
		e, ok := err.(withSilent)
		if !ok {
			fmt.Fprintln(os.Stderr, e)
		}
	}
	rootCmd.AddCommand(pkgCmd)
	rootCmd.Execute()
}

func initConfig() {
	if cfgFile == "" {
		cfgFile, _ = config.UserConfigFile()
	}
	viper.SetConfigFile(cfgFile)
	viper.ReadInConfig()
}

type viperConfigStore struct{}

const packageInstallPathConfigEnv = "TOIT_PACKAGE_INSTALL_PATH"
const configKeyRegistries = "pkg.registries"
const configKeyAutosync = "pkg.autosync"

func (vc *viperConfigStore) Load(ctx context.Context) (*commands.Config, error) {
	result := commands.Config{}

	if cacheDir == "" {
		var err error
		result.PackageCachePaths, err = config.PackageCachePaths()
		if err != nil {
			return nil, err
		}
		result.RegistryCachePaths, err = config.RegistryCachePaths()
		if err != nil {
			return nil, err
		}
	} else {
		result.PackageCachePaths = []string{filepath.Join(cacheDir, "tpkg")}
		result.RegistryCachePaths = []string{filepath.Join(cacheDir, "tpkg-registries")}
	}
	if p, ok := os.LookupEnv(packageInstallPathConfigEnv); ok {
		result.PackageInstallPath = &p
	}
	if sdkVersion != "" {
		v, err := version.NewVersion(sdkVersion)
		if err != nil {
			return nil, err
		}
		result.SDKVersion = v
	}

	if viper.IsSet(configKeyRegistries) {
		err := viper.UnmarshalKey(configKeyRegistries, &result.RegistryConfigs)
		if err != nil {
			return nil, err
		}
		if result.RegistryConfigs == nil {
			// Viper seems to just ignore empty lists.
			result.RegistryConfigs = tpkg.RegistryConfigs{}
		}
	} else if noDefaultRegistry {
		result.RegistryConfigs = tpkg.RegistryConfigs{}
	}

	if noAutosync {
		sync := false
		result.Autosync = &sync
	} else if viper.IsSet(configKeyAutosync) {
		sync := viper.GetBool(configKeyAutosync)
		result.Autosync = &sync
	}

	return &result, nil
}

func (vc *viperConfigStore) Store(ctx context.Context, cfg *commands.Config) error {
	if cfg.Autosync != nil {
		viper.Set(configKeyAutosync, *cfg.Autosync)
	}
	if cfg.RegistryConfigs != nil {
		viper.Set(configKeyRegistries, cfg.RegistryConfigs)
	}
	return viper.WriteConfig()
}
