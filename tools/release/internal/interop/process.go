package interop

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const maxChildOutput = 2 << 20

type privateBuffer struct {
	bytes.Buffer
	overflow bool
}

func (buffer *privateBuffer) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > maxChildOutput {
		buffer.overflow = true
		return 0, errors.New("private integration child output exceeds bounds")
	}
	return buffer.Buffer.Write(data)
}

type child struct {
	command *exec.Cmd
	done    chan error
}

func startChild(command *exec.Cmd) (*child, error) {
	configureChild(command)
	if err := command.Start(); err != nil {
		return nil, errors.New("integration child could not start")
	}
	process := &child{command, make(chan error, 1)}
	go func() { process.done <- command.Wait(); close(process.done) }()
	return process, nil
}
func (process *child) stop() {
	select {
	case <-process.done:
		return
	default:
	}
	signalChild(process.command, false)
	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		signalChild(process.command, true)
		<-process.done
	}
}
func runChild(ctx context.Context, command *exec.Cmd) ([]byte, int, error) {
	var output privateBuffer
	command.Stdout = &output
	command.Stderr = io.Discard
	process, err := startChild(command)
	if err != nil {
		return nil, -1, err
	}
	defer process.stop()
	select {
	case <-ctx.Done():
		return nil, -1, errors.New("integration exceeded its 60-second deadline")
	case <-process.done:
		if output.overflow {
			return nil, -1, errors.New("integration child output exceeds bounds")
		}
		return output.Bytes(), command.ProcessState.ExitCode(), nil
	}
}
func environment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, item := range os.Environ() {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	result := []string{}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
}
func secretPipe(value []byte) (*os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, errors.New("private integration pipe could not be created")
	}
	_, err = writer.Write(value)
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		_ = reader.Close()
		return nil, errors.New("private integration pipe could not be populated")
	}
	return reader, nil
}
