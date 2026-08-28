package examples

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssubedir/draincheck/internal/config"
	"go.yaml.in/yaml/v4"
)

func TestPipelineExamplesAreValidAndRetainFailureEvidence(t *testing.T) {
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: "github-actions.yaml",
			required: []string{
				"set -euo pipefail",
				"DRAINCHECK_VERSION: v0.2.0",
				"SHA256SUMS",
				"sha256sum --check --strict",
				"--report-json",
				"--report-junit",
				"--debug-bundle",
				"if: always()",
				"actions/upload-artifact@",
			},
		},
		{
			path: "gitlab-ci.yaml",
			required: []string{
				"github.com/ssubedir/draincheck/cmd/draincheck@v0.2.0",
				"--report-json",
				"--report-junit",
				"--debug-bundle",
				"when: always",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(data))
			var document any
			if err := decoder.Decode(&document); err != nil {
				t.Fatalf("decode pipeline YAML: %v", err)
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("pipeline must contain exactly one YAML document, got %v", err)
			}
			for _, required := range test.required {
				if !strings.Contains(string(data), required) {
					t.Errorf("pipeline is missing %q", required)
				}
			}
		})
	}
}

func TestStarterConfigurationIsValid(t *testing.T) {
	cfg, err := config.LoadFile("draincheck.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatalf("validate pilot starter configuration: %v", err)
	}
	if cfg.Target.Image != "checkout:local" || cfg.Target.ContainerPort != 8080 {
		t.Fatalf("unexpected pilot target: image=%q port=%d", cfg.Target.Image, cfg.Target.ContainerPort)
	}
	if cfg.Readiness.Path != "/ready" || cfg.Traffic.Request.Path != "/work?delay=2s" {
		t.Fatalf(
			"unexpected pilot endpoints: readiness=%q traffic=%q",
			cfg.Readiness.Path,
			cfg.Traffic.Request.Path,
		)
	}

}

func TestExternalPilotConfigurationsArePinnedAndValid(t *testing.T) {
	tests := []struct {
		name           string
		config         string
		image          string
		readinessPath  string
		trafficPath    string
		method         string
		signal         string
		requiredHeader string
	}{
		{
			name:          "go-httpbin",
			config:        "../testdata/pilot/external/go-httpbin/draincheck.yaml",
			image:         "ghcr.io/mccutchen/go-httpbin@sha256:20739736d4eb8dc1b998dff701f437b8bd62dcc46492bd0d861e89890ca36500",
			readinessPath: "/get",
			trafficPath:   "/delay/2s",
			method:        "GET",
			signal:        "SIGTERM",
		},
		{
			name:          "postman-httpbin",
			config:        "../testdata/pilot/external/postman-httpbin/draincheck.yaml",
			image:         "docker.io/kennethreitz/httpbin@sha256:599fe5e5073102dbb0ee3dbb65f049dab44fa9fc251f6835c9990f8fb196a72b",
			readinessPath: "/get",
			trafficPath:   "/delay/2",
			method:        "GET",
			signal:        "SIGTERM",
		},
		{
			name:           "mendhak-http-https-echo",
			config:         "../testdata/pilot/external/mendhak-http-https-echo/draincheck.yaml",
			image:          "ghcr.io/mendhak/http-https-echo@sha256:2046be25f4a2c0bdda662ebfb7c2b7b60fc95c31d97987be143645a8a2194a40",
			readinessPath:  "/draincheck-ready",
			trafficPath:    "/demo/orders",
			method:         "POST",
			signal:         "SIGTERM",
			requiredHeader: "X-Set-Response-Delay-Ms",
		},
		{
			name:           "traefik-whoami",
			config:         "../testdata/pilot/external/traefik-whoami/draincheck.yaml",
			image:          "docker.io/traefik/whoami@sha256:c4717a8d1f0134a7444e24f881160e033991f23027c6c5a9a3f8fd22e70d1d44",
			readinessPath:  "/health",
			trafficPath:    "/?wait=2s",
			method:         "GET",
			signal:         "SIGTERM",
			requiredHeader: "X-Forwarded-Proto",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.LoadFile(test.config)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(true); err != nil {
				t.Fatalf("validate external pilot config: %v", err)
			}
			if cfg.Target.Image != test.image {
				t.Errorf("external pilot image = %q, want %q", cfg.Target.Image, test.image)
			}
			if cfg.Readiness.Path != test.readinessPath || cfg.Traffic.Request.Path != test.trafficPath {
				t.Errorf("external pilot endpoints = %q and %q", cfg.Readiness.Path, cfg.Traffic.Request.Path)
			}
			if cfg.Traffic.Request.Method != test.method {
				t.Errorf("external pilot method = %q, want %q", cfg.Traffic.Request.Method, test.method)
			}
			if cfg.Shutdown.Signal != test.signal {
				t.Errorf("external pilot signal = %q, want %q", cfg.Shutdown.Signal, test.signal)
			}
			if test.requiredHeader != "" && cfg.Traffic.Request.Headers[test.requiredHeader] == "" {
				t.Errorf("external pilot request is missing header %q", test.requiredHeader)
			}
		})
	}
	dockerfiles, err := filepath.Glob("../testdata/pilot/external/*/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if len(dockerfiles) != 0 {
		t.Errorf("public-image pilots must not build derived images: %v", dockerfiles)
	}
}
