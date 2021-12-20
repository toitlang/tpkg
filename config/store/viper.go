package store

import (
	"context"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-version"
	"github.com/spf13/viper"
	"github.com/toitlang/tpkg/commands"
	"github.com/toitlang/tpkg/config"
	"github.com/toitlang/tpkg/pkg/tpkg"
)

type Viper struct {
	cacheDir          string
	sdkVersion        string
	noAutosync        bool
	noDefaultRegistry bool
}

func NewViper(cacheDir string, sdkVersion string, noAutosync bool, noDefaultRegistry bool) *Viper {
	return &Viper{
		cacheDir:          cacheDir,
		sdkVersion:        sdkVersion,
		noAutosync:        noAutosync,
		noDefaultRegistry: noDefaultRegistry,
	}
}

const packageInstallPathConfigEnv = "TOIT_PACKAGE_INSTALL_PATH"
const configKeyRegistries = "pkg.registries"
const configKeyAutosync = "pkg.autosync"

func (vc *Viper) Init(cfgFile string) error {
	viper.SetConfigFile(cfgFile)
	return viper.ReadInConfig()
}

func (vc *Viper) Load(ctx context.Context) (*commands.Config, error) {
	result := commands.Config{}

	if vc.cacheDir == "" {
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
		result.PackageCachePaths = []string{filepath.Join(vc.cacheDir, "tpkg")}
		result.RegistryCachePaths = []string{filepath.Join(vc.cacheDir, "tpkg-registries")}
	}
	if p, ok := os.LookupEnv(packageInstallPathConfigEnv); ok {
		result.PackageInstallPath = &p
	}
	if vc.sdkVersion != "" {
		v, err := version.NewVersion(vc.sdkVersion)
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
	} else if vc.noDefaultRegistry {
		result.RegistryConfigs = tpkg.RegistryConfigs{}
	}

	if vc.noAutosync {
		sync := false
		result.Autosync = &sync
	} else if viper.IsSet(configKeyAutosync) {
		sync := viper.GetBool(configKeyAutosync)
		result.Autosync = &sync
	}

	return &result, nil
}

func (vc *Viper) Store(ctx context.Context, cfg *commands.Config) error {
	if cfg.Autosync != nil {
		viper.Set(configKeyAutosync, *cfg.Autosync)
	}
	if cfg.RegistryConfigs != nil {
		viper.Set(configKeyRegistries, cfg.RegistryConfigs)
	}
	return viper.WriteConfig()
}
