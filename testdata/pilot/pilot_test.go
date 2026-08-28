package pilot

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type pilotCase struct {
	ID         string
	Language   string
	Directory  string
	Config     string
	BaseImages []string
}

var pilotCases = []pilotCase{
	{ID: "node", Language: "Node.js 24 LTS", Directory: "node", BaseImages: []string{"node:24-alpine"}},
	{ID: "python", Language: "Python 3.14", Directory: "python", BaseImages: []string{"python:3.14-alpine"}},
	{ID: "java", Language: "Java 21 LTS", Directory: "java", Config: "java/draincheck.yaml", BaseImages: []string{"eclipse-temurin:21-jdk-alpine", "eclipse-temurin:21-jre-alpine"}},
	{ID: "dotnet", Language: ".NET 10 LTS", Directory: "dotnet", BaseImages: []string{"mcr.microsoft.com/dotnet/sdk:10.0", "mcr.microsoft.com/dotnet/aspnet:10.0"}},
}

type pilotSummary struct {
	SchemaVersion  int           `json:"schema_version"`
	GeneratedAt    time.Time     `json:"generated_at"`
	Runtime        string        `json:"runtime"`
	RuntimeVersion string        `json:"runtime_version"`
	Passed         bool          `json:"passed"`
	Cases          []pilotResult `json:"cases"`
}

type pilotResult struct {
	ID         string   `json:"id"`
	Language   string   `json:"language"`
	BaseImages []string `json:"base_images"`
	ImageID    string   `json:"image_id,omitempty"`
	RunID      string   `json:"run_id,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	Passed     bool     `json:"passed"`
	Report     string   `json:"report"`
	Error      string   `json:"error,omitempty"`
}

type draincheckReport struct {
	SchemaVersion int                   `json:"schema_version"`
	RunID         string                `json:"run_id"`
	Runtime       string                `json:"runtime"`
	Passed        bool                  `json:"passed"`
	Events        []draincheckEvent     `json:"events"`
	Assertions    []draincheckAssertion `json:"assertions"`
	Traffic       draincheckTraffic     `json:"traffic"`
}

type draincheckEvent struct {
	Phase string `json:"phase"`
}

type draincheckAssertion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type draincheckTraffic struct {
	Configured int `json:"configured"`
	Started    int `json:"started"`
	Inflight   int `json:"inflight_at_signal"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
}

func TestPilotDefinitions(t *testing.T) {
	root := repositoryRoot(t)
	seen := make(map[string]bool, len(pilotCases))
	for _, definition := range pilotCases {
		if definition.ID == "" || definition.Language == "" || definition.Directory == "" || len(definition.BaseImages) == 0 {
			t.Errorf("incomplete pilot definition: %#v", definition)
		}
		if seen[definition.ID] {
			t.Errorf("duplicate pilot ID %q", definition.ID)
		}
		seen[definition.ID] = true
		for _, name := range []string{"Dockerfile"} {
			path := filepath.Join(root, "testdata", "pilot", definition.Directory, name)
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				t.Errorf("pilot %q requires regular file %s: %v", definition.ID, path, err)
			}
		}
		config := definition.Config
		if config == "" {
			config = "draincheck.yaml"
		}
		if info, err := os.Stat(filepath.Join(root, "testdata", "pilot", config)); err != nil || !info.Mode().IsRegular() {
			t.Errorf("pilot %q requires regular configuration %q: %v", definition.ID, config, err)
		}
	}
}

func TestSelectPilotCases(t *testing.T) {
	selected, err := selectPilotCases("python, node")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != "node" || selected[1].ID != "python" {
		t.Fatalf("selected pilots = %#v, want node then python", selected)
	}
	if _, err := selectPilotCases("unknown"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown selection error = %v", err)
	}
}

func TestRuntimePilots(t *testing.T) {
	runtimeName := environment("DRAINCHECK_PILOT_RUNTIME", "docker")
	if runtimeName != "docker" && runtimeName != "podman" {
		t.Fatalf("DRAINCHECK_PILOT_RUNTIME must be docker or podman, got %q", runtimeName)
	}
	runtimeBinary := os.Getenv("DRAINCHECK_PILOT_RUNTIME_BINARY")
	if runtimeBinary == "" {
		var err error
		runtimeBinary, err = exec.LookPath(runtimeName)
		if err != nil {
			t.Fatalf("find %s: %v", runtimeName, err)
		}
	} else {
		originalPath := os.Getenv("PATH")
		if err := os.Setenv("PATH", filepath.Dir(runtimeBinary)+string(os.PathListSeparator)+originalPath); err != nil {
			t.Fatalf("make %s available to Draincheck: %v", runtimeName, err)
		}
		t.Cleanup(func() { _ = os.Setenv("PATH", originalPath) })
	}
	if output, err := runCommand("", runtimeBinary, "info"); err != nil {
		t.Fatalf("check %s: %v\n%s", runtimeName, err, output)
	}
	runtimeVersion, err := runCommand("", runtimeBinary, "--version")
	if err != nil {
		t.Fatalf("read %s version: %v\n%s", runtimeName, err, runtimeVersion)
	}

	selected, err := selectPilotCases(os.Getenv("DRAINCHECK_PILOT_CASE"))
	if err != nil {
		t.Fatal(err)
	}
	root := repositoryRoot(t)
	reportDirectory := os.Getenv("DRAINCHECK_PILOT_REPORT_DIR")
	if reportDirectory == "" {
		reportDirectory = filepath.Join(t.TempDir(), "reports")
	} else if !filepath.IsAbs(reportDirectory) {
		reportDirectory = filepath.Join(root, reportDirectory)
	}
	if err := os.MkdirAll(reportDirectory, 0o755); err != nil {
		t.Fatalf("create pilot report directory: %v", err)
	}

	binaryName := "draincheck"
	if goruntime.GOOS == "windows" {
		binaryName += ".exe"
	}
	draincheckBinary := filepath.Join(t.TempDir(), binaryName)
	if output, err := runCommand(root, "go", "build", "-trimpath", "-o", draincheckBinary, "./cmd/draincheck"); err != nil {
		t.Fatalf("build Draincheck: %v\n%s", err, output)
	}

	results := make([]pilotResult, 0, len(selected))
	for _, definition := range selected {
		definition := definition
		var result pilotResult
		t.Run(definition.ID, func(t *testing.T) {
			result = runPilotCase(root, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, definition)
			if !result.Passed {
				t.Error(result.Error)
			}
		})
		results = append(results, result)
	}

	summary := pilotSummary{
		SchemaVersion:  1,
		GeneratedAt:    time.Now().UTC(),
		Runtime:        runtimeName,
		RuntimeVersion: firstLine(runtimeVersion),
		Passed:         len(results) > 0,
		Cases:          results,
	}
	for _, result := range results {
		if !result.Passed {
			summary.Passed = false
		}
	}
	if err := writeJSONAtomic(filepath.Join(reportDirectory, "summary.json"), summary); err != nil {
		t.Errorf("write pilot summary: %v", err)
	}
}

func runPilotCase(root, reportDirectory, draincheckBinary, runtimeBinary, runtimeName string, definition pilotCase) (result pilotResult) {
	started := time.Now()
	result = pilotResult{
		ID:         definition.ID,
		Language:   definition.Language,
		BaseImages: append([]string(nil), definition.BaseImages...),
		Report:     definition.ID + ".json",
	}
	image := fmt.Sprintf("draincheck-pilot-%s:%d-%d", definition.ID, os.Getpid(), time.Now().UnixNano())
	imageBuilt := false
	defer func() {
		if cleanupErr := removePilotContainers(runtimeBinary, runtimeName, image); cleanupErr != nil {
			result.Error = appendError(result.Error, cleanupErr.Error())
		}
		if imageBuilt {
			if output, err := runCommand("", runtimeBinary, "image", "rm", "--force", image); err != nil && !strings.Contains(strings.ToLower(output), "no such image") {
				result.Error = appendError(result.Error, fmt.Sprintf("remove pilot image: %v: %s", err, bounded(output)))
			}
		}
		result.DurationMS = time.Since(started).Milliseconds()
		result.Passed = result.Error == ""
	}()

	caseDirectory := filepath.Join(root, "testdata", "pilot", definition.Directory)
	buildOutput, err := runCommand(root, runtimeBinary,
		"build",
		"-f", filepath.Join(caseDirectory, "Dockerfile"),
		"-t", image,
		caseDirectory,
	)
	if err != nil {
		result.Error = fmt.Sprintf("build %s pilot image: %v\n%s", definition.ID, err, bounded(buildOutput))
		return result
	}
	imageBuilt = true
	imageID, inspectErr := runCommand("", runtimeBinary, "image", "inspect", "--format", "{{.Id}}", image)
	if inspectErr != nil {
		result.Error = appendError(result.Error, fmt.Sprintf("inspect pilot image: %v: %s", inspectErr, bounded(imageID)))
	} else {
		result.ImageID = strings.TrimSpace(imageID)
	}

	jsonPath := filepath.Join(reportDirectory, definition.ID+".json")
	junitPath := filepath.Join(reportDirectory, definition.ID+".xml")
	debugPath := filepath.Join(reportDirectory, definition.ID+"-debug.zip")
	for _, path := range []string{jsonPath, junitPath, debugPath} {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			result.Error = appendError(result.Error, fmt.Sprintf("remove stale evidence %s: %v", filepath.Base(path), removeErr))
			return result
		}
	}
	config := definition.Config
	if config == "" {
		config = "draincheck.yaml"
	}
	verifyOutput, verifyErr := runCommand(root, draincheckBinary,
		"verify", image,
		"--runtime", runtimeName,
		"--config", filepath.Join(root, "testdata", "pilot", config),
		"--report-json", jsonPath,
		"--report-junit", junitPath,
		"--debug-bundle", debugPath,
		"--no-color",
	)
	if verifyErr != nil {
		result.Error = appendError(result.Error, fmt.Sprintf("Draincheck verification failed: %v\n%s", verifyErr, bounded(verifyOutput)))
	}

	report, reportErr := readDraincheckReport(jsonPath)
	if reportErr != nil {
		result.Error = appendError(result.Error, reportErr.Error())
		return result
	}
	result.RunID = report.RunID
	for _, validationErr := range validateDraincheckReport(report, runtimeName) {
		result.Error = appendError(result.Error, validationErr)
	}
	for _, path := range []string{junitPath, debugPath} {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			result.Error = appendError(result.Error, fmt.Sprintf("missing or empty evidence file %s: %v", filepath.Base(path), statErr))
		}
	}
	return result
}

func selectPilotCases(filter string) ([]pilotCase, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" || filter == "all" {
		return append([]pilotCase(nil), pilotCases...), nil
	}
	requested := make(map[string]bool)
	for _, value := range strings.Split(filter, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			requested[value] = true
		}
	}
	selected := make([]pilotCase, 0, len(requested))
	for _, definition := range pilotCases {
		if requested[definition.ID] {
			selected = append(selected, definition)
			delete(requested, definition.ID)
		}
	}
	if len(requested) > 0 {
		unknown := make([]string, 0, len(requested))
		for value := range requested {
			unknown = append(unknown, value)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown DRAINCHECK_PILOT_CASE value(s): %s", strings.Join(unknown, ", "))
	}
	return selected, nil
}

func readDraincheckReport(path string) (draincheckReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return draincheckReport{}, fmt.Errorf("read Draincheck report: %w", err)
	}
	var report draincheckReport
	if err := json.Unmarshal(data, &report); err != nil {
		return draincheckReport{}, fmt.Errorf("decode Draincheck report: %w", err)
	}
	return report, nil
}

func validateDraincheckReport(report draincheckReport, runtimeName string) []string {
	var failures []string
	if report.SchemaVersion != 1 {
		failures = append(failures, fmt.Sprintf("report schema version = %d, want 1", report.SchemaVersion))
	}
	if report.RunID == "" {
		failures = append(failures, "report run ID is empty")
	}
	if report.Runtime != runtimeName {
		failures = append(failures, fmt.Sprintf("report runtime = %q, want %q", report.Runtime, runtimeName))
	}
	if !report.Passed {
		failures = append(failures, "lifecycle report did not pass")
	}
	if report.Traffic.Configured < 1 || report.Traffic.Started != report.Traffic.Configured || report.Traffic.Completed != report.Traffic.Configured {
		failures = append(failures, fmt.Sprintf("traffic coverage = configured %d, started %d, completed %d", report.Traffic.Configured, report.Traffic.Started, report.Traffic.Completed))
	}
	if report.Traffic.Inflight < 1 {
		failures = append(failures, "no request remained in flight at signal confirmation")
	}
	if report.Traffic.Failed != 0 {
		failures = append(failures, fmt.Sprintf("traffic failures = %d, want 0", report.Traffic.Failed))
	}
	if len(report.Assertions) == 0 {
		failures = append(failures, "report contains no assertions")
	}
	for _, assertion := range report.Assertions {
		if !assertion.Passed {
			failures = append(failures, "failed assertion: "+assertion.Name)
		}
	}
	phases := make(map[string]bool)
	for _, event := range report.Events {
		phases[event.Phase] = true
	}
	for _, required := range []string{"ready", "terminating", "exited", "cleanup"} {
		if !phases[required] {
			failures = append(failures, "report is missing lifecycle phase: "+required)
		}
	}
	return failures
}

func removePilotContainers(runtimeBinary, runtimeName, image string) error {
	output, err := runCommand("", runtimeBinary, "ps", "-aq", "--filter", "label=io.draincheck.run", "--filter", "ancestor="+image)
	if err != nil {
		return fmt.Errorf("inspect pilot cleanup: %w: %s", err, bounded(output))
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return nil
	}
	args := []string{"rm", "--force"}
	if runtimeName == "podman" {
		args = append(args, "--time", "0")
	}
	args = append(args, ids...)
	cleanupOutput, cleanupErr := runCommand("", runtimeBinary, args...)
	return fmt.Errorf("Draincheck left containers behind: %v (exact-ID cleanup: %v, %s)", ids, cleanupErr, bounded(cleanupOutput))
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pilot-*")
	if err != nil {
		return fmt.Errorf("create temporary summary: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temporary summary: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temporary summary: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary summary: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace summary: %w", removeErr)
		}
		if retryErr := os.Rename(temporary, path); retryErr != nil {
			return fmt.Errorf("replace summary: %w", retryErr)
		}
	}
	return nil
}

func runCommand(directory, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	return string(output), err
}

func appendError(current, addition string) string {
	if strings.TrimSpace(addition) == "" {
		return current
	}
	if current == "" {
		return addition
	}
	return current + "\n" + addition
}

func bounded(value string) string {
	const limit = 4000
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:] + "\n[earlier output truncated]"
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate pilot test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
