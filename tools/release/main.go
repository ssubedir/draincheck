package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ssubedir/draincheck/internal/release"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "package":
		err = runPackage(os.Args[2:])
	case "checksums":
		err = runChecksums(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runPackage(arguments []string) error {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	version := flags.String("version", "", "v-prefixed semantic version")
	commit := flags.String("commit", "", "source commit")
	dateText := flags.String("date", "", "RFC3339 build date")
	architectureText := flags.String("arch", "amd64,arm64", "comma-separated target architectures")
	root := flags.String("root", ".", "repository root")
	output := flags.String("output", "dist", "artifact output directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("package accepts no positional arguments")
	}
	buildDate, err := time.Parse(time.RFC3339, *dateText)
	if err != nil {
		return fmt.Errorf("date must use RFC3339: %w", err)
	}
	architectures := strings.Split(*architectureText, ",")
	for index := range architectures {
		architectures[index] = strings.TrimSpace(architectures[index])
	}
	artifacts, err := release.Package(context.Background(), release.PackageOptions{
		Root:          *root,
		OutputDir:     *output,
		Version:       *version,
		Commit:        *commit,
		BuildDate:     buildDate,
		Architectures: architectures,
	})
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		fmt.Println(artifact)
	}
	return nil
}

func runChecksums(arguments []string) error {
	flags := flag.NewFlagSet("checksums", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	output := flags.String("output", "dist", "artifact output directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("checksums accepts no positional arguments")
	}
	path, err := release.WriteChecksums(*output)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/release package|checksums [flags]")
}
