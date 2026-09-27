package container

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestInitConfigPipe(t *testing.T) {
	want := &initConfig{
		RootfsPath: "/bundle/rootfs",
		Spec: &specs.Spec{
			Version: "1.3.0",
			Root:    &specs.Root{Path: "rootfs"},
			Process: &specs.Process{Args: []string{"/bin/sh"}, Cwd: "/"},
		},
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close() })
	written := make(chan error, 1)
	go func() {
		err := writeInitMessage(writer, want)
		_ = writer.CloseWithError(err)
		written <- err
	}()

	got, err := readInitConfig(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestInitResult(t *testing.T) {
	for _, want := range []initResult{
		{Ready: true},
		{Error: "pivot root: operation not permitted"},
	} {
		var buffer bytes.Buffer
		if err := writeInitMessage(&buffer, want); err != nil {
			t.Fatal(err)
		}
		got, err := readInitResult(&buffer)
		if err != nil {
			t.Fatal(err)
		}
		if *got != want {
			t.Fatalf("result = %+v, want %+v", *got, want)
		}
	}
}

func TestInitProtocolRejectsInvalidMessages(t *testing.T) {
	for name, input := range map[string]string{
		"empty":       "",
		"truncated":   `{"ready":`,
		"null":        "null",
		"array":       "[]",
		"two objects": `{"ready":true}{"ready":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readInitConfig(strings.NewReader(input)); err == nil {
				t.Fatal("invalid config message accepted")
			}
			if _, err := readInitResult(strings.NewReader(input)); err == nil {
				t.Fatal("invalid result message accepted")
			}
		})
	}
	for _, input := range []string{
		`{}`,
		`{"rootfsPath":"/rootfs"}`,
		`{"rootfsPath":"relative","spec":{}}`,
		`{"rootfsPath":"/rootfs\u0000","spec":{}}`,
	} {
		if _, err := readInitConfig(strings.NewReader(input)); err == nil {
			t.Fatalf("invalid config accepted: %s", input)
		}
	}
	for _, input := range []string{
		`{}`,
		`{"ready":false}`,
		`{"ready":true,"error":"failed"}`,
	} {
		if _, err := readInitResult(strings.NewReader(input)); err == nil {
			t.Fatalf("invalid result accepted: %s", input)
		}
	}
}

func TestInitProtocolPreservesIOErrors(t *testing.T) {
	failure := errors.New("broken pipe")
	if err := writeInitMessage(failingIO{failure}, initResult{Ready: true}); !errors.Is(err, failure) {
		t.Fatalf("write error = %v, want %v", err, failure)
	}
	if _, err := readInitConfig(failingIO{failure}); !errors.Is(err, failure) {
		t.Fatalf("read error = %v, want %v", err, failure)
	}
}

type failingIO struct{ err error }

func (f failingIO) Read([]byte) (int, error)  { return 0, f.err }
func (f failingIO) Write([]byte) (int, error) { return 0, f.err }
