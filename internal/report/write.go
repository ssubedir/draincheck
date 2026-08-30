package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func WriteJSON(path string, value *Report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON report: %w", err)
	}
	return writeAtomic(path, append(data, '\n'))
}

func WriteJUnit(path string, value *Report) error {
	suite := junitSuite{
		Name:     "draincheck",
		Tests:    len(value.Assertions),
		Failures: len(value.FailedAssertions()),
		Time:     strconv.FormatFloat(float64(value.DurationMS)/1000, 'f', 3, 64),
	}
	for _, assertion := range value.Assertions {
		item := junitCase{Name: assertion.Name, Classname: "draincheck.lifecycle"}
		if !assertion.Passed {
			item.Failure = &junitFailure{Message: assertion.Message, Body: assertion.Message}
		}
		suite.Cases = append(suite.Cases, item)
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JUnit report: %w", err)
	}
	data = append([]byte(xml.Header), data...)
	data = append(data, '\n')
	return writeAtomic(path, data)
}

func WriteFile(path string, data []byte) error {
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil { // #nosec G301 -- CI report directories contain non-secret artifacts.
		return fmt.Errorf("create report directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".draincheck-*")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	tempName := file.Name()
	defer func() { _ = os.Remove(tempName) }()

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary report: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary report: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary report: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return fmt.Errorf("replace report %q: %w", path, err)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("replace report %q: %w", path, err)
		}
		if retryErr := os.Rename(tempName, path); retryErr != nil {
			return fmt.Errorf("replace report %q: %w", path, retryErr)
		}
	}
	return nil
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
