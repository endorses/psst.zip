// Package process runs build tools with explicit cancellation and bounded output.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner carries the working checkout and optional environment overrides.
type Runner struct {
	Dir      string
	Env      map[string]string
	CleanEnv bool // Exclude ambient credentials/configuration from child processes.
}

func (r Runner) With(env map[string]string) Runner {
	merged := make(map[string]string, len(r.Env)+len(env))
	for k, v := range r.Env {
		merged[k] = v
	}
	for k, v := range env {
		merged[k] = v
	}
	r.Env = merged
	return r
}

// Result contains bounded private subprocess output. Callers may inspect it to
// produce fixed diagnostic categories, but must never include it in errors/logs.
type Result struct{ Stdout, Stderr []byte }

// Error never contains arguments, working directories, OS error text or captured
// output. Consumers can report a fixed operation/category while retaining useful
// exit and cancellation information.
type Error struct {
	Operation  string
	ExitCode   int
	Kind       string
	Permission bool
	cause      error
}

func (e *Error) Error() string {
	if e.Kind == "exit" {
		return fmt.Sprintf("%s command failed (exit %d); output withheld", e.Operation, e.ExitCode)
	}
	return fmt.Sprintf("%s command failed (%s); output withheld", e.Operation, e.Kind)
}
func (e *Error) Unwrap() error { return e.cause }

// Result executes one bounded command. A failed command still returns bounded
// output for trusted classification; neither Error nor Output exposes that text.
func (r Runner) Result(ctx context.Context, timeout time.Duration, args ...string) (Result, error) {
	var stdout limitedBuffer
	stderr, err := r.execute(ctx, timeout, &stdout, func() bool { return stdout.exceeded }, args...)
	return Result{Stdout: stdout.Bytes(), Stderr: stderr}, err
}

// RunTo streams a process output without retaining an archive in memory. The
// caller must close its destination on cancellation and enforce any tighter
// format limits. A universal 8 GiB output cap and bounded private stderr remain.
func (r Runner) RunTo(ctx context.Context, timeout time.Duration, destination io.Writer, args ...string) error {
	if destination == nil {
		return fmt.Errorf("missing build output destination")
	}
	output := &streamWriter{Writer: destination, remaining: 8 << 30}
	_, err := r.execute(ctx, timeout, output, func() bool { return output.exceeded }, args...)
	return err
}

func (r Runner) execute(ctx context.Context, timeout time.Duration, stdout io.Writer, exceeded func() bool, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing build command")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = r.Dir
	values := map[string]string{}
	if !r.CleanEnv {
		for _, item := range os.Environ() {
			if k, v, ok := strings.Cut(item, "="); ok {
				values[k] = v
			}
		}
	}
	for k, v := range r.Env {
		values[k] = v
	}
	cmd.Env = make([]string, 0, len(values))
	for k, v := range values {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	sort.Strings(cmd.Env)
	var stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stderr.Bytes(), nil
	}
	failure := &Error{Operation: filepath.Base(args[0]), ExitCode: -1, Kind: "start", Permission: errors.Is(err, os.ErrPermission)}
	if ctx.Err() != nil {
		failure.Kind, failure.cause = "cancelled", ctx.Err()
	} else if exceeded() || stderr.exceeded {
		failure.Kind = "output-limit"
	} else {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			failure.Kind, failure.ExitCode = "exit", exit.ExitCode()
		}
	}
	return stderr.Bytes(), failure
}

type streamWriter struct {
	io.Writer
	remaining int64
	exceeded  bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.exceeded = true
		return 0, fmt.Errorf("stream output exceeds limit")
	}
	n, err := w.Writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// Output never includes command output in errors: callers may process credentials.
func (r Runner) Output(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	result, err := r.Result(ctx, timeout, args...)
	if err != nil {
		return nil, err
	}
	return result.Stdout, nil
}

type limitedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<20 {
		b.exceeded = true
		return 0, fmt.Errorf("build output exceeds limit")
	}
	return b.Buffer.Write(p)
}
