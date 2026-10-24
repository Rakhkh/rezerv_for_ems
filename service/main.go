package main

import (
	"fmt"
	"io"
	"os"
)

const (
	exitUsage = 2
	exitFatal = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stderr))
}

func run(args []string, lookupEnv func(string) (string, bool), stderr io.Writer) int {
	if _, err := parseConfig(args, lookupEnv); err != nil {
		fmt.Fprintf(stderr, "ghreport: %s\n", err)
		return exitUsage
	}

	// Stage 2 replaces this boundary with the first complete collection scenario.
	fmt.Fprintln(stderr, "ghreport: сбор отчёта ещё не реализован")
	return exitFatal
}
