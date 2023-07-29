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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/alessio/shellescape"
	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
	"github.com/toitlang/tpkg/pkg/tpkg"
	"github.com/toitlang/tpkg/pkg/tracking"
)

const ConfigKeyRegistries = "pkg.registries"
const ConfigKeyAutosync = "pkg.autosync"

type ConfigStore interface {
	Load(ctx context.Context) (*Config, error)
	Store(ctx context.Context, cfg *Config) error
}

type Config struct {
	PackageCachePaths  []string
	RegistryCachePaths []string
	PackageInstallPath *string
	SDKVersion         *version.Version

	// The following entries must be `nil` if they are not set in the
	// configuration.
	// Note that viper changes empty lists to `nil` so it's important to
	// check for that case.
	RegistryConfigs tpkg.RegistryConfigs
}

var defaultRegistry = tpkg.RegistryConfig{
	Name: "toit",
	Kind: tpkg.RegistryKindGit,
	Path: "github.com/toitware/registry",
}

func (h *pkgHandler) getRegistryConfigsOrDefault() tpkg.RegistryConfigs {
	if h.hasRegistryConfigs() {
		return h.cfg.RegistryConfigs
	}
	return []tpkg.RegistryConfig{defaultRegistry}
}

func (h *pkgHandler) hasRegistryConfigs() bool {
	return h.cfg.RegistryConfigs != nil
}

func (h *pkgHandler) saveRegistryConfigs(ctx context.Context, configs tpkg.RegistryConfigs) error {
	h.cfg.RegistryConfigs = configs
	return h.saveConfigs(ctx)
}

func (h *pkgHandler) saveConfigs(ctx context.Context) error {
	return h.cfgStore.Store(ctx, h.cfg)
}

type CobraCommand func(cmd *cobra.Command, args []string)
type CobraErrorCommand func(cmd *cobra.Command, args []string) error
type Run func(CobraErrorCommand) CobraCommand

type Registries tpkg.Registries

func (h *pkgHandler) buildCache() (tpkg.Cache, error) {
	pkgCachePaths := h.cfg.PackageCachePaths
	registryCachePaths := h.cfg.RegistryCachePaths
	registryPath, registryCachePaths := registryCachePaths[0], registryCachePaths[1:]
	options := []tpkg.CacheOption{
		tpkg.WithPkgCachePath(pkgCachePaths...),
		tpkg.WithRegistryCachePath(registryCachePaths...),
	}

	if h.cfg.PackageInstallPath != nil {
		options = append(options, tpkg.WithPkgInstallPath(*h.cfg.PackageInstallPath))
	}

	return tpkg.NewCache(registryPath, h.ui, options...), nil
}

func (h *pkgHandler) buildManager(ctx context.Context, cmd *cobra.Command) (*tpkg.Manager, error) {
	cache, err := h.buildCache()
	if err != nil {
		return nil, err
	}
	shouldAutoSync, err := cmd.Flags().GetBool("auto-sync")
	if err != nil {
		return nil, err
	}

	registries, err := h.loadUserRegistries(ctx, shouldAutoSync, cache)
	if err != nil {
		return nil, err
	}
	sdkVersion := h.cfg.SDKVersion
	if err != nil {
		return nil, err
	}
	return tpkg.NewManager(tpkg.Registries(registries), cache, sdkVersion, h.ui, h.track), nil
}

func (h *pkgHandler) buildProjectPkgManager(cmd *cobra.Command, shouldSyncRegistries bool) (*tpkg.ProjectPkgManager, error) {
	projectRoot, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return nil, err
	}
	manager, err := h.buildManager(cmd.Context(), cmd)
	if err != nil {
		return nil, err
	}
	paths, err := tpkg.NewProjectPaths(projectRoot, "", "")
	if err != nil {
		return nil, err
	}
	return tpkg.NewProjectPkgManager(manager, paths), nil
}

type pkgHandler struct {
	cfg      *Config
	cfgStore ConfigStore
	ui       tpkg.UI
	track    tracking.Track
}

func Pkg(run Run, track tracking.Track, configStore ConfigStore, ui tpkg.UI) (*cobra.Command, error) {

	if ui == nil {
		ui = tpkgUI
	}

	handler := &pkgHandler{
		cfgStore: configStore,
		ui:       ui,
		track:    track,
	}

	// 1. Loads the config before invoking the command.
	// 2. Intercepts any error and checks if it is an already-reported error.
	//    If it is, replaces it with a silent error.
	//    Otherwise returns it to the caller.
	// 3. Wraps the call into the given 'run' function.
	errorCfgRun := func(f CobraErrorCommand) CobraCommand {
		return run(func(cmd *cobra.Command, args []string) error {
			if handler.cfg == nil {
				cfg, err := handler.cfgStore.Load(cmd.Context())
				if err != nil {
					return err
				}
				handler.cfg = cfg
			}

			sdkVersion, err := cmd.Flags().GetString("sdk-version")
			if err != nil {
				return err
			}
			if sdkVersion != "" {
				v, err := version.NewVersion(sdkVersion)
				if err != nil {
					return err
				}
				handler.cfg.SDKVersion = v
			}

			err = f(cmd, args)

			if tpkg.IsErrAlreadyReported(err) {
				return newExitError(1)
			}
			return err
		})
	}

	cmd := &cobra.Command{
		Use:   "pkg",
		Short: "Manage packages",
	}
	cmd.PersistentFlags().String("project-root", "", "specify the project root")
	cmd.PersistentFlags().Bool("auto-sync", true, "automatically synchronize registries")
	cmd.PersistentFlags().String("sdk-version", "", "specify the SDK version")

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Creates a new package and lock file in the current directory",
		Long: `Initializes the current directory as the root of the project.

This is done by creating a 'package.lock' and 'package.yaml' file.

If the --project-root flag is used, initializes that directory instead.`,
		Run:  errorCfgRun(handler.pkgInit),
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(initCmd)

	installCmd := &cobra.Command{
		Use:   "install [<package>]",
		Short: "Installs a package in the current project, or downloads all dependencies",
		Long: `If no 'package' is given, then the command downloads all dependencies.
If necessary, updates the lock-file. This can happen if the lock file doesn't exist
yet, or if the lock-file has local path dependencies (which could have their own
dependencies changed). Recomputation of the dependencies can also be forced by
providing the '--recompute' flag.

If a 'package' is given finds the package with the given name or URL and installs it.
The given 'package' string must uniquely identify a package in the registry.
It is matched against all package names, and URLs. For the names, a package is considered
a match if the string is equal. For URLs it is a match if the string is a complete match, or
the '/' + string is a suffix of the URL.

The 'package' may be suffixed by a version with a '@' separating the package name and
the version. The version doesn't need to be complete. For example 'foo@2' installs
the package foo with the highest version satisfying '2.0.0 <= version < 3.0.0'.
Note: the version constraint in the package.yaml is set to accept semver compatible
versions. If necessary, modify the constraint in that file.

Installed packages are identified by their name. If the '--name' argument is
provided, that one is used instead. Packages can then be used by
  'import <name>.<lib>'.

If the '--local' flag is used, then the 'package' argument is interpreted as
a local path to a package directory. Note that published packages may not
contain local packages.
`,
		Example: `  # Assumes that the package 'toitware/toit-morse' has the
  # name 'morse' and has a library 'morse.toit' in its 'src' folder.

  # Ensures all dependencies are downloaded.
  toit pkg install

  # Install package named 'morse'. The installed name is 'morse' (the package name).
  # Programs would import this package with 'import morse.morse'
  #   which can be shortened to 'import morse'.
  toit pkg install morse

  # Install the package 'morse' with an alternative name.
  # Programs would use this package with 'import alt_morse.morse'.
  toit pkg install morse --name=alt_morse

  # Install the version 1.0.0 of the package 'morse'.
  toit pkg install morse@1.0.0

  # Install the package 'morse' by URL (to disambiguate). The longer the URL
  # the less likely a conflict.
  # Programs would import this package with 'import morse'.
  toit pkg install toitware/toit-morse
  toit pkg install github.com/toitware/toit-morse

  # Install the package 'morse' by URL with a given name.
  # Programs would use this package with 'import alt_morse.morse'.
  toit pkg install toitware/toit-morse --name=alt_morse

  # Install a local package folder by path.
  toit pkg install --local ../my_other_package
  toit pkg install --local submodules/my_other_package --name=other
`,
		Run:     errorCfgRun(handler.pkgInstall),
		Args:    cobra.MaximumNArgs(1),
		Aliases: []string{"download", "fetch"},
	}
	installCmd.Flags().Bool("local", false, "Treat package argument as local path")
	installCmd.Flags().Bool("recompute", false, "Recompute dependencies")
	installCmd.Flags().String("name", "", "The name used for the 'import' clause")
	cmd.AddCommand(installCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "uninstall <name>",
		Short: "Uninstalls the package with the given name",
		Long: `Uninstalls the package with the given name.

Removes the package of the given name from the package files.
The downloaded code is not automatically deleted.
`,
		Run:  errorCfgRun(handler.pkgUninstall),
		Args: cobra.ExactArgs(1),
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Updates all packages to their newest versions",
		Long: `Updates all packages to their newest compatible version.

Uses semantic versioning to find the highest compatible version
of each imported package (and their transitive dependencies).
It then updates all packages to these versions.
`,
		Run:  errorCfgRun(handler.pkgUpdate),
		Args: cobra.NoArgs,
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "clean",
		Short: "Removes unnecessary packages",
		// TODO(florian): also add "strip" and "tidy" versions.
		Long: `Removes unnecessary packages.

If a package isn't used anymore removes the downloaded files from the
local package cache.
`,
		Run:  errorCfgRun(handler.pkgClean),
		Args: cobra.NoArgs,
	})

	cmd.AddCommand(&cobra.Command{
		Use:    "lockfile",
		Short:  "Prints the content of the lockfile",
		Run:    errorCfgRun(handler.printLockFile),
		Args:   cobra.NoArgs,
		Hidden: true,
	})

	cmd.AddCommand(&cobra.Command{
		Use:    "packagefile",
		Short:  "Prints the content of package.yaml",
		Run:    errorCfgRun(handler.printPackageFile),
		Args:   cobra.NoArgs,
		Hidden: true,
	})

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Lists all available packages",
		Long: `Lists all packages.

If no argument is given, lists all available packages.
If an argument is given, it must point to a registry path. In that case
only the packages from that registry are shown.`,
		Run:  errorCfgRun(handler.pkgList),
		Args: cobra.MaximumNArgs(1),
	}
	listCmd.Flags().BoolP("verbose", "v", false, "Show more information")
	listCmd.Flags().StringP("output", "o", "list", "Defines the output format (valid: 'list', 'json')")
	cmd.AddCommand(listCmd)

	searchCmd := &cobra.Command{
		Use:   "search <name>",
		Short: "Searches for the given name in all packages",
		Long: `Searches for the given 'name'.

Searches in the name, and description entries, as well as in the URLs of
the packages.`,
		Run:  errorCfgRun(handler.pkgSearch),
		Args: cobra.ExactArgs(1),
	}
	searchCmd.Flags().BoolP("verbose", "v", false, "Show more information")
	cmd.AddCommand(searchCmd)

	registryCmd := &cobra.Command{
		Use:   "registry",
		Short: "Manages registries",
	}
	cmd.AddCommand(registryCmd)

	addRegistryCmd := &cobra.Command{
		Use:   "add <name> <URL>",
		Short: "Adds a registry",
		Long: `Adds a registry.

The 'name' of the registry must not be used yet.

By default the 'URL' is interpreted as Git-URL.
If the '--local' flag is used, then the 'URL' is interpreted as local
path to a folder containing package descriptions.`,
		Example: `  # Add the toit registry.
  toit pkg registry add toit github.com/toitware/registry
`,
		Run:  errorCfgRun(handler.pkgRegistryAdd),
		Args: cobra.ExactArgs(2),
	}
	addRegistryCmd.Flags().Bool("local", false, "Registry is local")
	registryCmd.AddCommand(addRegistryCmd)

	removeRegistryCmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Removes a registry",
		Long: `Removes a registry.

The 'name' of the registry you want to remove.`,
		Example: `  # Remove the toit registry.
  toit pkg registry remove toit
`,
		Run:  errorCfgRun(handler.pkgRegistryRemove),
		Args: cobra.ExactArgs(1),
	}
	registryCmd.AddCommand(removeRegistryCmd)

	syncRegistryCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronizes all registries",
		Long: `Synchronizes registries.

If no argument is given, synchronizes all registries.
If an argument is given, it must point to a registry path. In that case
only that registry is synchronized.`,
		Run:  errorCfgRun(handler.pkgRegistrySync),
		Args: cobra.ArbitraryArgs,
	}
	syncRegistryCmd.Flags().BoolP("clear-cache", "", false, "Clear the registry cache before synchronizing")
	registryCmd.AddCommand(syncRegistryCmd)

	listRegistriesCmd := &cobra.Command{
		Use:   "list",
		Short: "List registries",
		Run:   errorCfgRun(handler.pkgRegistriesList),
		Args:  cobra.NoArgs,
	}

	registryCmd.AddCommand(listRegistriesCmd)

	syncToplevelCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronizes all registries",
		Long: `Synchronizes all registries.

This is an alias for 'pkg registry sync'`,
		Run:  errorCfgRun(handler.pkgRegistrySync),
		Args: cobra.NoArgs,
	}
	syncToplevelCmd.Flags().BoolP("clear-cache", "", false, "Clear the registry cache before synchronizing")
	cmd.AddCommand(syncToplevelCmd)

	describeCmd := &cobra.Command{
		Use:   "describe [<path_or_url>] [<version>]",
		Short: "Generates a description of the given package",
		Long: `Generates a description of the given package.

If no 'path' is given, defaults to the current working directory.
If one argument is given, then it must be a path to a package.
Otherwise, the first argument is interpreted as the URL to the package, and
the second argument must be a version.

A package description is used when publishing packages. It describes the
package to the outside world. This command extracts a description from
the given path.

If the out directory is specified, generates a description file as used
by registries. The actual description file is generated nested in
directories to make the description path unique.`,
		Run:  errorCfgRun(handler.pkgDescribe),
		Args: cobra.MaximumNArgs(2),
	}
	describeCmd.Flags().BoolP("verbose", "v", false, "Show more information")
	describeCmd.Flags().String("out-dir", "", "Output directory of description files")
	describeCmd.Flags().Bool("allow-local-deps", false, "Allow local dependencies and don't report them")
	describeCmd.Flags().Bool("disallow-local-deps", false, "Always disallow local dependencies and report them as error")
	cmd.AddCommand(describeCmd)

	return cmd, nil
}

type exitError struct {
	code int
}

func (e *exitError) ExitCode() int {
	return e.code
}

func (e *exitError) Silent() bool {
	return true
}

func (e *exitError) Error() string {
	return fmt.Sprintf("ExitError - exit code: %d", e.code)
}

func newExitError(code int) *exitError {
	return &exitError{
		code: code,
	}
}

var tpkgUI = tpkg.FmtUI

func (h *pkgHandler) pkgInstall(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	shouldAutoSync, err := cmd.Flags().GetBool("auto-sync")
	if err != nil {
		return err
	}
	m, err := h.buildProjectPkgManager(cmd, shouldAutoSync)

	if err != nil {
		return err
	}
	projectRoot, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return err
	}
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		if cwd != m.Paths.ProjectRootPath {
			// Add the project-root flag, and rebuild the command line.
			args := os.Args
			args = append(args, "--project-root="+m.Paths.ProjectRootPath)
			quoted := []string{}
			for _, arg := range args {
				quoted = append(quoted, shellescape.Quote(arg))
			}
			withFlag := strings.Join(quoted, " ")
			h.ui.ReportError(`Command must be executed in project root.
  Run 'pkg init' first to create a new application here, or
  Run with '--project-root': ` + withFlag)
			return newExitError(1)
		}
	}
	isLocal, err := cmd.Flags().GetBool("local")
	if err != nil {
		return err
	}
	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return err
	}
	forceRecompute, err := cmd.Flags().GetBool("recompute")
	if err != nil {
		return err
	}

	if len(args) == 0 {
		if isLocal {
			h.ui.ReportError("Local flag requires path argument")
			return newExitError(1)
		}
		if name != "" {
			h.ui.ReportError("Name flag can only be used with a package argument")
			return newExitError(1)
		}
		err = m.Install(ctx, forceRecompute)

		h.track(ctx, &tracking.Event{
			Name: "toit pkg install",
			Properties: map[string]string{
				"recompute": strconv.FormatBool(forceRecompute),
			},
		})

		if err != nil {
			return err

		}
		return nil
	}

	if forceRecompute {
		h.ui.ReportError("The '--recompute' flag  can only be used without arguments")
	}

	installedName := ""
	pkgString := ""

	if isLocal {
		p := args[0]
		installedName, err = m.InstallLocalPkg(ctx, name, p)
		pkgString = p
		if err != nil {
			return err
		}
	} else {
		id := args[0]
		installedName, pkgString, err = m.InstallURLPkg(ctx, name, id)
		if err != nil {
			return err
		}
	}

	tpkgUI.ReportInfo("Package '%s' installed with name '%s'", pkgString, installedName)

	h.track(ctx, &tracking.Event{
		Name: "toit pkg install",
		Properties: map[string]string{
			"package":      pkgString,
			"install_name": installedName,
		},
	})

	return nil
}

func (h *pkgHandler) pkgUninstall(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	m, err := h.buildProjectPkgManager(cmd, false)
	if err != nil {
		return err
	}
	return m.Uninstall(ctx, args[0])

}

func (h *pkgHandler) pkgUpdate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	shouldAutoSync, err := cmd.Flags().GetBool("auto-sync")
	if err != nil {
		return err
	}
	m, err := h.buildProjectPkgManager(cmd, shouldAutoSync)
	if err != nil {
		return err
	}
	return m.Update(ctx)
}

func (h *pkgHandler) pkgClean(cmd *cobra.Command, args []string) error {
	m, err := h.buildProjectPkgManager(cmd, false)
	if err != nil {
		return err
	}
	return m.CleanPackages()
}

func (h *pkgHandler) printLockFile(cmd *cobra.Command, args []string) error {
	m, err := h.buildProjectPkgManager(cmd, false)
	if err != nil {
		return err
	}
	return m.PrintLockFile()
}

func (h *pkgHandler) printPackageFile(cmd *cobra.Command, args []string) error {
	m, err := h.buildProjectPkgManager(cmd, false)
	if err != nil {
		return err
	}
	return m.PrintSpecFile()
}

func (h *pkgHandler) pkgInit(cmd *cobra.Command, args []string) error {
	projectRoot, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return err
	}

	err = tpkg.InitDirectory(projectRoot, tpkgUI)
	if IsAlreadyExistsError(err) {
		return h.ui.ReportError(ErrorMessage(err))
	} else if err != nil {
		return err

	}
	return nil
}

// Loads all registries as specified by the user's configuration.
func (h *pkgHandler) loadUserRegistries(ctx context.Context, shouldAutoSync bool, cache tpkg.Cache) ([]tpkg.Registry, error) {
	configs := h.getRegistryConfigsOrDefault()
	return configs.Load(ctx, shouldAutoSync, cache, h.ui)
}

func printDesc(d *tpkg.Desc, indent string, isVerbose bool, isJson bool) {
	if isJson {
		md, err := json.Marshal(d)
		if err != nil {
			log.Fatal("Unexpected error marshaling description. %w", err)
		}
		fmt.Println(string(md))
		return
	}
	if !isVerbose {
		fmt.Printf("%s%s - %s\n", indent, d.Name, d.Version)
		return
	}
	tmpl := template.Must(template.New("description").Parse(`{{.Name}}:
  description: {{.Description}}
  url: {{.URL}}
  version: {{.Version}}
  {{if .Environment.SDK}}environment:
    sdk: {{.Environment.SDK}}
  {{end}}{{if .License}}license: {{.License}}
  {{end}}{{if .Hash}}hash: {{.Hash}}
  {{end}}{{if .Deps }}Dependencies:{{ range $_, $d := .Deps }}
    {{$d.URL}} - {{$d.Version}}{{ end}}{{end}}`))
	out := bytes.Buffer{}
	if err := tmpl.Execute(&out, d); err != nil {
		log.Fatal("Unexpected error while using template. %w", err)
	}
	str := out.String()
	// Add the indentation.
	lines := strings.Split(str, "\n")
	for _, line := range lines {
		fmt.Printf("%s%s\n", indent, line)
	}
}

func (h *pkgHandler) pkgList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	cache, err := h.buildCache()
	if err != nil {
		return err
	}
	shouldAutoSync, err := cmd.Flags().GetBool("auto-sync")
	if err != nil {
		return err
	}
	registries, err := h.loadUserRegistries(ctx, shouldAutoSync, cache)
	if err != nil {
		return err
	}
	isVerbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return err
	}
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	isJson := output == "json"

	if len(args) == 1 {
		registry := tpkg.NewLocalRegistry("", args[0])
		sync := false
		if err := registry.Load(ctx, sync, cache, h.ui); err != nil {
			if !tpkg.IsErrAlreadyReported(err) {
				return h.ui.ReportError("Error while loading registry '%s': %v", args[0], err)
			}
			return err
		}
		registries = []tpkg.Registry{
			registry,
		}
	}
	for _, registry := range registries {
		fmt.Printf("%s:\n", registry.Describe())
		for _, desc := range registry.Entries() {
			printDesc(desc, "  ", isVerbose, isJson)
		}
	}
	return nil
}

func (h *pkgHandler) pkgRegistriesList(cmd *cobra.Command, args []string) error {
	configs := h.getRegistryConfigsOrDefault()
	for _, config := range configs {
		fmt.Printf("%s: %s (%s)\n", config.Name, config.Path, config.Kind)
	}
	return nil
}

func (h *pkgHandler) pkgRegistryAdd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	cache, err := h.buildCache()
	if err != nil {
		return err
	}
	isLocal, err := cmd.Flags().GetBool("local")
	if err != nil {
		return err
	}
	name := args[0]
	pathOrURL := args[1]
	var kind tpkg.RegistryKind = tpkg.RegistryKindGit
	if isLocal {
		kind = tpkg.RegistryKindLocal
		abs, err := filepath.Abs(pathOrURL)
		if err != nil {
			h.ui.ReportError("Invalid registry: %v", err)
			return newExitError(1)
		}
		info, err := os.Stat(abs)
		if os.IsNotExist(err) {
			h.ui.ReportError("Path doesn't exist: %v", err)
			return newExitError(1)
		} else if !info.IsDir() {
			h.ui.ReportError("Path isn't a directory: %v", err)
			return newExitError(1)
		}
		pathOrURL = abs
	}
	configs := h.getRegistryConfigsOrDefault()
	// Check that we don't already have a registry with that name.
	for _, config := range configs {
		if config.Name == name {
			if config.Kind != kind || config.Path != pathOrURL {
				h.ui.ReportError("Registry '%s' already exists", name)
				return newExitError(1)
			}
			// Already exists with the same config.
			if h.hasRegistryConfigs() {
				return nil
			}
			// Already exists, but not saved in the configuration file.
			// Not strictly necessary, but if the user explicitly adds a configuration
			// we want to write it into the config file.
			return h.saveRegistryConfigs(ctx, configs)
		}
	}
	registryConfig := tpkg.RegistryConfig{
		Name: name,
		Kind: kind,
		Path: pathOrURL,
	}
	trackProperties := map[string]string{
		"name": name,
		"kind": string(kind),
	}
	if kind == tpkg.RegistryKindGit {
		trackProperties["url"] = pathOrURL
	}
	h.track(ctx, &tracking.Event{
		Name:       "toit pkg registry add",
		Properties: trackProperties,
	})

	sync := true
	clearCache := false
	_, err = registryConfig.Load(ctx, sync, clearCache, cache, h.ui)

	if err != nil {
		if !tpkg.IsErrAlreadyReported(err) {
			return h.ui.ReportError("Registry '%s' has errors: %v", name, err)
		}
		return err
	}
	configs = append(configs, registryConfig)
	return h.saveRegistryConfigs(ctx, configs)
}

func (h *pkgHandler) pkgRegistryRemove(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	name := args[0]
	configs := h.getRegistryConfigsOrDefault()
	index := -1
	for i, config := range configs {
		if config.Name == name {
			index = i
			break
		}
	}

	if index == -1 {
		h.ui.ReportError("Registry '%s' does not exist", name)
		return newExitError(1)
	}

	h.track(cmd.Context(), &tracking.Event{
		Name: "toit pkg registry remove",
		Properties: map[string]string{
			"name": configs[index].Name,
			"path": configs[index].Path,
		},
	})

	configs = append(configs[0:index], configs[index+1:]...)
	return h.saveRegistryConfigs(ctx, configs)
}

func (h *pkgHandler) pkgRegistrySync(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	clearCache, err := cmd.Flags().GetBool("clear-cache")
	if err != nil {
		return err
	}
	cache, err := h.buildCache()
	if err != nil {
		return err
	}
	configs := h.getRegistryConfigsOrDefault()

	var configsToSync []tpkg.RegistryConfig

	syncAll := len(args) == 0
	if syncAll {
		configsToSync = configs
	} else {
		nameToConfig := map[string]tpkg.RegistryConfig{}
		for _, config := range configs {
			nameToConfig[config.Name] = config
		}
		for _, toSyncName := range args {
			config, ok := nameToConfig[toSyncName]
			if !ok {
				h.ui.ReportWarning("Config '%s' not found", toSyncName)
			} else {
				configsToSync = append(configsToSync, config)
			}
		}
	}

	hasErrors := false
	for _, config := range configsToSync {
		sync := true
		h.ui.ReportInfo("Syncing '%s'", config.Name)
		_, err := config.Load(ctx, sync, clearCache, cache, h.ui)
		if err != nil {
			if !tpkg.IsErrAlreadyReported(err) {
				h.ui.ReportError("Error while syncing '%s': '%v'", config.Name, err)
			} else {
				h.ui.ReportError("Error while syncing '%s'", config.Name)
			}
			hasErrors = true
		}
	}
	if hasErrors {
		return newExitError(1)
	}
	return nil
}

func (h *pkgHandler) pkgSearch(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	h.track(ctx, &tracking.Event{
		Name: "toit pkg search",
		Properties: map[string]string{
			"query": args[0],
		},
	})

	cache, err := h.buildCache()
	if err != nil {
		return err
	}
	shouldAutoSync, err := cmd.Flags().GetBool("auto-sync")
	if err != nil {
		return err
	}
	registries, err := h.loadUserRegistries(ctx, shouldAutoSync, cache)
	if err != nil {
		return err
	}
	isVerbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return err
	}

	found, err := tpkg.Registries(registries).SearchAll(args[0])
	if err != nil {
		return err
	}
	found, err = found.WithoutLowerVersions()
	if err != nil {
		return err
	}
	for _, descReg := range found {
		printDesc(descReg.Desc, "", isVerbose, false)
	}
	return nil
}

func (h *pkgHandler) pkgDescribe(cmd *cobra.Command, args []string) error {
	isVerbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return err
	}
	outDir, err := cmd.Flags().GetString("out-dir")
	if err != nil {
		return err
	}
	var desc *tpkg.Desc
	if len(args) < 2 && outDir != "" {
		h.ui.ReportError("The --out-dir flag requires a URL and version")
		return newExitError(1)
	}

	allowFlag, err := cmd.Flags().GetBool("allow-local-deps")
	if err != nil {
		return err
	}
	disallowFlag, err := cmd.Flags().GetBool("disallow-local-deps")
	if err != nil {
		return err
	}

	if allowFlag && disallowFlag {
		h.ui.ReportError("--allow-local-deps and --disallow-local-deps are exclusive")
		return newExitError(1)
	}

	var allowsLocalDeps = tpkg.ReportLocalDeps
	if allowFlag {
		allowsLocalDeps = tpkg.AllowLocalDeps
	} else if disallowFlag || len(args) >= 2 {
		allowsLocalDeps = tpkg.DisallowLocalDeps
	}

	if len(args) == 0 {
		var cwd string
		cwd, err = os.Getwd()
		if err != nil {
			return err
		}

		desc, err = tpkg.ScrapeDescriptionAt(cwd, allowsLocalDeps, isVerbose, h.ui)
	} else if len(args) == 1 {
		desc, err = tpkg.ScrapeDescriptionAt(args[0], allowsLocalDeps, isVerbose, h.ui)
	} else {
		h.track(cmd.Context(), &tracking.Event{
			Name: "toit pkg describe",
			Properties: map[string]string{
				"url":     args[0],
				"version": args[1],
			},
		})

		ctx := cmd.Context()
		desc, err = tpkg.ScrapeDescriptionGit(ctx, args[0], args[1], allowsLocalDeps, isVerbose, h.ui)
	}

	if err != nil {
		return err
	}
	if outDir == "" {
		printDesc(desc, "", true, false)
		return nil
	}
	descPath, err := desc.WriteInDir(outDir)
	if err != nil {
		return err
	}
	h.ui.ReportInfo("Wrote '%s'", descPath)
	return nil
}
