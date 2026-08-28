package repetition

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
		return fmt.Errorf("marshal repeat JSON summary: %w", err)
	}
	if err := report.WriteFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write repeat JSON summary: %w", err)
	}
	return nil
}

func WriteJUnit(path string, value *Summary) error {
	unevaluatedBudgets := 0
	for _, assertion := range value.BudgetAssertions {
		if !assertion.Evaluated {
			unevaluatedBudgets++
		}
	}
	suite := repeatJUnitSuite{
		Name:     "draincheck-repeat",
		Tests:    value.RunsRequested + len(value.BudgetAssertions),
		Failures: value.RunsFailed - value.ExecutionErrors + value.BudgetFailures,
		Errors:   value.ExecutionErrors,
		Skipped:  value.RunsRequested - value.RunsCompleted + unevaluatedBudgets,
		Time:     strconv.FormatFloat(float64(value.DurationMS)/1000, 'f', 3, 64),
	}
	for _, run := range value.Runs {
		item := repeatJUnitCase{
			Name:      fmt.Sprintf("run-%03d", run.Index),
			Classname: "draincheck.repeat",
			Time:      strconv.FormatFloat(float64(run.DurationMS)/1000, 'f', 3, 64),
		}
		if run.Error != "" {
			item.Error = &repeatJUnitProblem{Message: run.Error, Body: run.Error}
		} else if !run.Passed {
			message := "failed assertions: " + strings.Join(run.FailedAssertions, ", ")
			item.Failure = &repeatJUnitProblem{Message: message, Body: message}
		}
		suite.Cases = append(suite.Cases, item)
	}
	for index := value.RunsCompleted + 1; index <= value.RunsRequested; index++ {
		suite.Cases = append(suite.Cases, repeatJUnitCase{
			Name:      fmt.Sprintf("run-%03d", index),
			Classname: "draincheck.repeat",
			Skipped:   &struct{}{},
		})
	}
	for _, assertion := range value.BudgetAssertions {
		item := repeatJUnitCase{
			Name:      assertion.Name,
			Classname: "draincheck.repeat.budget",
		}
		if !assertion.Evaluated {
			item.Skipped = &struct{}{}
		} else if !assertion.Passed {
			item.Failure = &repeatJUnitProblem{Message: assertion.Message, Body: assertion.Message}
		}
		suite.Cases = append(suite.Cases, item)
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal repeat JUnit summary: %w", err)
	}
	data = append([]byte(xml.Header), data...)
	if err := report.WriteFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write repeat JUnit summary: %w", err)
	}
	return nil
}

type repeatJUnitSuite struct {
	XMLName  xml.Name          `xml:"testsuite"`
	Name     string            `xml:"name,attr"`
	Tests    int               `xml:"tests,attr"`
	Failures int               `xml:"failures,attr"`
	Errors   int               `xml:"errors,attr"`
	Skipped  int               `xml:"skipped,attr"`
	Time     string            `xml:"time,attr"`
	Cases    []repeatJUnitCase `xml:"testcase"`
}

type repeatJUnitCase struct {
	Name      string              `xml:"name,attr"`
	Classname string              `xml:"classname,attr"`
	Time      string              `xml:"time,attr,omitempty"`
	Failure   *repeatJUnitProblem `xml:"failure,omitempty"`
	Error     *repeatJUnitProblem `xml:"error,omitempty"`
	Skipped   *struct{}           `xml:"skipped,omitempty"`
}

type repeatJUnitProblem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
