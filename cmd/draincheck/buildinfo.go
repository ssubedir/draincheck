package main

import (
	"runtime/debug"

	"github.com/ssubedir/draincheck/internal/cli"
)

const developmentModuleVersion = "(devel)"

func resolveBuildInfo(build cli.BuildInfo, info *debug.BuildInfo) cli.BuildInfo {
	if info == nil {
		return build
	}

	if isDevelopmentValue(build.Version, "dev") &&
		info.Main.Version != "" && info.Main.Version != developmentModuleVersion {
		build.Version = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if isDevelopmentValue(build.Commit, "unknown") && setting.Value != "" {
				build.Commit = setting.Value
			}
		case "vcs.time":
			if isDevelopmentValue(build.Date, "unknown") && setting.Value != "" {
				build.Date = setting.Value
			}
		}
	}

	return build
}

func isDevelopmentValue(value, fallback string) bool {
	return value == "" || value == fallback
}
