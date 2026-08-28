package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestInitThenValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draincheck.yaml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"init", "--image", "fixture:test", "--port", "8080", "--output", path}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("init code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), []string{"validate", "--config", path}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("validate code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is valid") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestValidateReturnsUsageExitCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"validate", "--config", path}, &stdout, &stderr, BuildInfo{})
	if code != 2 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestLifecycleCommandsRejectUnknownProfile(t *testing.T) {
	for _, name := range []string{"validate", "verify", "repeat", "suite"} {
		command := commandForProfileTest(name)
		flag := command.Flags().Lookup("profile")
		if flag == nil || flag.DefValue != "generic" {
			t.Errorf("%s --profile flag = %#v", name, flag)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"validate", "--profile", "nomad"}, &stdout, &stderr, BuildInfo{})
	if code != 2 || !strings.Contains(stderr.String(), "profile must be generic or kubernetes") {
		t.Fatalf("invalid profile code/stderr = %d/%q", code, stderr.String())
	}
}

func commandForProfileTest(name string) *cobra.Command {
	switch name {
	case "validate":
		return newValidateCommand(&bytes.Buffer{})
	case "verify":
		return newVerifyCommand(&bytes.Buffer{})
	case "repeat":
		return newRepeatCommand(&bytes.Buffer{})
	case "suite":
		return newSuiteCommand(&bytes.Buffer{})
	default:
		panic("unsupported command")
	}
}

func TestParseByteSize(t *testing.T) {
	value, err := parseByteSize("1MiB")
	if err != nil || value != 1<<20 {
		t.Fatalf("parseByteSize = %d, %v", value, err)
	}
}

func TestVerifyExposesDebugBundleFlag(t *testing.T) {
	command := newVerifyCommand(&bytes.Buffer{})
	flag := command.Flags().Lookup("debug-bundle")
	if flag == nil {
		t.Fatal("verify command is missing --debug-bundle")
	}
	if flag.DefValue != "" {
		t.Fatalf("--debug-bundle default = %q, want empty", flag.DefValue)
	}
	if flag := command.Flags().Lookup("profile"); flag == nil || flag.DefValue != "generic" {
		t.Fatalf("verify --profile flag = %#v", flag)
	}
}

func TestRepeatCommandDefaultsAndRunBounds(t *testing.T) {
	command := newRepeatCommand(&bytes.Buffer{})
	if flag := command.Flags().Lookup("runs"); flag == nil || flag.DefValue != "3" {
		t.Fatalf("repeat --runs flag = %#v, want default 3", flag)
	}
	if flag := command.Flags().Lookup("report-dir"); flag == nil || flag.DefValue != "reports/draincheck-repeat" {
		t.Fatalf("repeat --report-dir flag = %#v", flag)
	}
	if flag := command.Flags().Lookup("profile"); flag == nil || flag.DefValue != "generic" {
		t.Fatalf("repeat --profile flag = %#v", flag)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"repeat", "--runs", "1"}, &stdout, &stderr, BuildInfo{})
	if code != 2 || !strings.Contains(stderr.String(), "runs must be between 2 and 100") {
		t.Fatalf("repeat bounds code/stderr = %d/%q", code, stderr.String())
	}
}

func TestSuiteCommandDefaultsAndScenarioBounds(t *testing.T) {
	command := newSuiteCommand(&bytes.Buffer{})
	if flag := command.Flags().Lookup("config"); flag == nil || flag.DefValue != "[]" {
		t.Fatalf("suite --config flag = %#v", flag)
	}
	if flag := command.Flags().Lookup("report-dir"); flag == nil || flag.DefValue != "reports/draincheck-suite" {
		t.Fatalf("suite --report-dir flag = %#v", flag)
	}
	if flag := command.Flags().Lookup("profile"); flag == nil || flag.DefValue != "generic" {
		t.Fatalf("suite --profile flag = %#v", flag)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"suite", "--config", "one.yaml"}, &stdout, &stderr, BuildInfo{})
	if code != 2 || !strings.Contains(stderr.String(), "suite requires between 2 and 100") {
		t.Fatalf("suite bounds code/stderr = %d/%q", code, stderr.String())
	}
}

func TestSuiteScenarioNameUsesPortableYAMLStem(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: filepath.Join("scenarios", "http.yaml"), want: "http"},
		{path: filepath.Join("scenarios", "grpc-health.YML"), want: "grpc-health"},
		{path: "orders.v2.yaml", want: "orders.v2"},
	} {
		got, err := suiteScenarioName(test.path)
		if err != nil || got != test.want {
			t.Errorf("suiteScenarioName(%q) = %q, %v; want %q", test.path, got, err, test.want)
		}
	}
	for _, path := range []string{"scenario.json", "bad name.yaml", "-leading.yaml"} {
		if _, err := suiteScenarioName(path); err == nil {
			t.Errorf("suiteScenarioName(%q) succeeded", path)
		}
	}
}

func TestLoadSuiteScenariosRejectsDuplicateNamesAndImages(t *testing.T) {
	root := t.TempDir()
	firstDirectory := filepath.Join(root, "first")
	secondDirectory := filepath.Join(root, "second")
	if err := os.MkdirAll(firstDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(firstDirectory, "http.yaml")
	duplicate := filepath.Join(secondDirectory, "HTTP.yaml")
	writeSuiteTestConfig(t, first, "first:test")
	writeSuiteTestConfig(t, duplicate, "first:test")
	if _, _, err := loadSuiteScenarios([]string{first, duplicate}, "", "generic"); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate-name error = %v", err)
	}

	second := filepath.Join(secondDirectory, "grpc.yaml")
	writeSuiteTestConfig(t, second, "second:test")
	if _, _, err := loadSuiteScenarios([]string{first, second}, "", "generic"); err == nil || !strings.Contains(err.Error(), "every suite scenario") {
		t.Fatalf("image-mismatch error = %v", err)
	}
	scenarios, image, err := loadSuiteScenarios([]string{first, second}, "override:test", "generic")
	if err != nil {
		t.Fatal(err)
	}
	if image != "override:test" || len(scenarios) != 2 || scenarios[0].config.Target.Image != image || scenarios[1].config.Target.Image != image {
		t.Fatalf("overridden scenarios = %q/%#v", image, scenarios)
	}
}

func TestSuiteValidatesEveryConfigBeforeResolvingRuntime(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.yaml")
	invalid := filepath.Join(directory, "invalid.yaml")
	writeSuiteTestConfig(t, valid, "example:test")
	if err := os.WriteFile(invalid, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"suite",
		"--config", valid,
		"--config", invalid,
		"--runtime", "invalid-runtime",
	}, &stdout, &stderr, BuildInfo{})
	if code != 2 || !strings.Contains(stderr.String(), "load scenario \"invalid\"") || strings.Contains(stderr.String(), "runtime must be") {
		t.Fatalf("suite prevalidation code/stderr = %d/%q", code, stderr.String())
	}
}

func writeSuiteTestConfig(t *testing.T, path, image string) {
	t.Helper()
	content := "version: 1\ntarget:\n  image: " + image + "\n  container_port: 8080\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
