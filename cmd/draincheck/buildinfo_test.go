package main

import (
	"runtime/debug"
	"testing"

	"github.com/ssubedir/draincheck/internal/cli"
)

func TestResolveBuildInfoUsesModuleVersionForGoInstall(t *testing.T) {
	build := resolveBuildInfo(cli.BuildInfo{
		Version: "dev",
		Commit:  "unknown",
		Date:    "unknown",
	}, &debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/ssubedir/draincheck",
			Version: "v0.1.0",
		},
	})

	if build.Version != "v0.1.0" {
		t.Fatalf("version = %q, want v0.1.0", build.Version)
	}
	if build.Commit != "unknown" {
		t.Fatalf("commit = %q, want unknown", build.Commit)
	}
	if build.Date != "unknown" {
		t.Fatalf("date = %q, want unknown", build.Date)
	}
}

func TestResolveBuildInfoPreservesReleaseLinkerMetadata(t *testing.T) {
	want := cli.BuildInfo{
		Version: "v1.2.3",
		Commit:  "release-commit",
		Date:    "2026-08-29T04:02:55Z",
	}
	build := resolveBuildInfo(want, &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "checkout-commit"},
			{Key: "vcs.time", Value: "2025-01-01T00:00:00Z"},
		},
	})

	if build != want {
		t.Fatalf("build info = %#v, want %#v", build, want)
	}
}

func TestResolveBuildInfoUsesVCSMetadataForDevelopmentBuild(t *testing.T) {
	build := resolveBuildInfo(cli.BuildInfo{
		Version: "dev",
		Commit:  "unknown",
		Date:    "unknown",
	}, &debug.BuildInfo{
		Main: debug.Module{Version: developmentModuleVersion},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "checkout-commit"},
			{Key: "vcs.time", Value: "2026-08-29T05:00:00Z"},
		},
	})

	if build.Version != "dev" {
		t.Fatalf("version = %q, want dev", build.Version)
	}
	if build.Commit != "checkout-commit" {
		t.Fatalf("commit = %q, want checkout-commit", build.Commit)
	}
	if build.Date != "2026-08-29T05:00:00Z" {
		t.Fatalf("date = %q, want embedded VCS time", build.Date)
	}
}

func TestResolveBuildInfoHandlesMissingBuildInfo(t *testing.T) {
	want := cli.BuildInfo{Version: "dev", Commit: "unknown", Date: "unknown"}
	if build := resolveBuildInfo(want, nil); build != want {
		t.Fatalf("build info = %#v, want %#v", build, want)
	}
}
