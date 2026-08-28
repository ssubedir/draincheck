package suite

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/ssubedir/draincheck/internal/report"
)

func WriteJSON(path string, value *Summary) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suite JSON summary: %w", err)
	}
	if err := report.WriteFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write suite JSON summary: %w", err)
	}
	return nil
}

func WriteJUnit(path string, value *Summary) error {
	suite := suiteJUnitSuite{
		Name:     "draincheck-suite",
		Tests:    value.ScenariosRequested,
		Failures: value.ScenariosFailed - value.ExecutionErrors,
		Errors:   value.ExecutionErrors,
		Skipped:  value.ScenariosRequested - value.ScenariosCompleted,
		Time:     strconv.FormatFloat(float64(value.DurationMS)/1000, 'f', 3, 64),
	}
	for _, scenario := range value.Scenarios {
		item := suiteJUnitCase{
			Name:      scenario.Name,
			Classname: "draincheck.suite",
			Time:      strconv.FormatFloat(float64(scenario.DurationMS)/1000, 'f', 3, 64),
		}
		if scenario.Error != "" {
			item.Error = &suiteJUnitProblem{Message: scenario.Error, Body: scenario.Error}
		} else if !scenario.Passed {
			message := "failed assertions: " + strings.Join(scenario.FailedAssertions, ", ")
			item.Failure = &suiteJUnitProblem{Message: message, Body: message}
		}
		suite.Cases = append(suite.Cases, item)
	}
	for _, name := range missingScenarioNames(value) {
		suite.Cases = append(suite.Cases, suiteJUnitCase{
			Name:      name,
			Classname: "draincheck.suite",
			Skipped:   &struct{}{},
		})
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suite JUnit summary: %w", err)
	}
	data = append([]byte(xml.Header), data...)
	if err := report.WriteFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write suite JUnit summary: %w", err)
	}
	return nil
}

func missingScenarioNames(value *Summary) []string {
	missing := make([]string, 0, value.ScenariosRequested-value.ScenariosCompleted)
	for index := value.ScenariosCompleted; index < value.ScenariosRequested; index++ {
		missing = append(missing, value.planned[index].Name)
	}
	return missing
}

type suiteJUnitSuite struct {
	XMLName  xml.Name         `xml:"testsuite"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Cases    []suiteJUnitCase `xml:"testcase"`
}

type suiteJUnitCase struct {
	Name      string             `xml:"name,attr"`
	Classname string             `xml:"classname,attr"`
	Time      string             `xml:"time,attr,omitempty"`
	Failure   *suiteJUnitProblem `xml:"failure,omitempty"`
	Error     *suiteJUnitProblem `xml:"error,omitempty"`
	Skipped   *struct{}          `xml:"skipped,omitempty"`
}

type suiteJUnitProblem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
