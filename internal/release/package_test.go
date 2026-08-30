package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWriteArchiveIsReproducibleAndNormalizesMetadata(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "binary")
	readme := filepath.Join(directory, "README.md")
	if err := os.WriteFile(binary, []byte("binary-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readme, []byte("documentation"), 0o600); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.FixedZone("offset", -4*60*60))
	entries := []archiveEntry{
		{name: "README.md", path: readme, mode: 0o644},
		{name: "draincheck", path: binary, mode: 0o755},
	}
	first := filepath.Join(directory, "first.tar.gz")
	second := filepath.Join(directory, "second.tar.gz")
	if err := writeArchive(first, entries, timestamp.UTC()); err != nil {
		t.Fatal(err)
	}
	if err := writeArchive(second, entries, timestamp.UTC()); err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstData, secondData) {
		t.Fatal("archives from identical inputs differ")
	}

	file, err := os.Open(first) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	if !gzipReader.ModTime.Equal(timestamp.UTC()) {
		t.Errorf("gzip timestamp = %s, want %s", gzipReader.ModTime, timestamp.UTC())
	}
	tarReader := tar.NewReader(gzipReader)
	var names []string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(timestamp.UTC()) {
			t.Errorf("entry %q metadata = uid %d gid %d time %s", header.Name, header.Uid, header.Gid, header.ModTime)
		}
		if header.Name == "draincheck" && header.Mode != 0o755 {
			t.Errorf("binary mode = %#o, want 0755", header.Mode)
		}
	}
	if !reflect.DeepEqual(names, []string{"README.md", "draincheck"}) {
		t.Fatalf("archive entries = %v", names)
	}
}

func TestWriteChecksumsIsSortedAndExcludesSignatureBundle(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{
		"draincheck_linux_arm64.tar.gz":    "arm64",
		"draincheck_linux_amd64.tar.gz":    "amd64",
		"draincheck_linux_amd64.spdx.json": "sbom",
		"draincheck_ignored.sigstore.json": "signature",
		"unrelated.txt":                    "unrelated",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := WriteChecksums(directory)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantNames := []string{
		"draincheck_linux_amd64.spdx.json",
		"draincheck_linux_amd64.tar.gz",
		"draincheck_linux_arm64.tar.gz",
	}
	if len(lines) != len(wantNames) {
		t.Fatalf("checksum lines = %q", lines)
	}
	for index, name := range wantNames {
		hash := sha256.Sum256([]byte(files[name]))
		want := fmt.Sprintf("%x  %s", hash, name)
		if lines[index] != want {
			t.Errorf("checksum line %d = %q, want %q", index, lines[index], want)
		}
	}
}

func TestPackageEntriesShipSupportContract(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("license"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := packageEntries(root, filepath.Join(root, "draincheck"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	want := []string{"LICENSE", "README.md", "docs/support.md", "draincheck", "schema/draincheck.schema.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("release entries = %v, want %v", names, want)
	}
}

func TestPackageEntriesRequireLicense(t *testing.T) {
	root := t.TempDir()
	if _, err := packageEntries(root, filepath.Join(root, "draincheck")); err == nil || !strings.Contains(err.Error(), "require release license") {
		t.Fatalf("packageEntries() error = %v, want missing license error", err)
	}
}

func TestRepositoryLicenseIsCanonicalApache20(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	got := fmt.Sprintf("%x", sha256.Sum256(normalized))
	const want = "dbf00e67ea3d79687196d06c87fcfe0b86c36b355e68b99ae1ae13010dc9ab7b"
	if got != want {
		t.Fatalf("LICENSE sha256 = %s, want canonical project Apache-2.0 text %s", got, want)
	}
}

func TestValidatePackageOptionsRejectsUnsupportedTargets(t *testing.T) {
	options := PackageOptions{
		Root:          ".",
		OutputDir:     "dist",
		Version:       "v1.2.3",
		Commit:        "abc123",
		BuildDate:     time.Now(),
		Architectures: []string{"386"},
	}
	if err := validatePackageOptions(options); err == nil || !strings.Contains(err.Error(), "386") {
		t.Fatalf("validation error = %v, want unsupported architecture", err)
	}
	options.Architectures = []string{"amd64"}
	options.Version = "latest"
	if err := validatePackageOptions(options); err == nil || !strings.Contains(err.Error(), "semantic version") {
		t.Fatalf("validation error = %v, want semantic version", err)
	}
	options.Version = "v1.2.3+build.1"
	if err := validatePackageOptions(options); err == nil || !strings.Contains(err.Error(), "build metadata") {
		t.Fatalf("validation error = %v, want build metadata rejection", err)
	}
}

func TestBuildEnvironmentOverridesTargetDeterministically(t *testing.T) {
	environment := buildEnvironment([]string{"PATH=/tools", "GOOS=windows", "GOARCH=386", "CGO_ENABLED=1"}, "arm64")
	want := []string{"PATH=/tools", "CGO_ENABLED=0", "GOARCH=arm64", "GOOS=linux"}
	if !reflect.DeepEqual(environment, want) {
		t.Fatalf("build environment = %#v, want %#v", environment, want)
	}
}
