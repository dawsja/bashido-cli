//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func fail(code int, format string, args ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

type app struct {
	in         io.Reader
	out        io.Writer
	errOut     io.Writer
	getenv     func(string) string
	executable func() (string, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{in: os.Stdin, out: os.Stdout, errOut: os.Stderr, getenv: os.Getenv, executable: os.Executable}
	if err := a.run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "bashido: %v\n", err)
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
