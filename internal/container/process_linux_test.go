package container

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestStartInit(t *testing.T) {
	// 构建真正的 minirunc，确保测试经过 CLI 分流和内部入口，而不是
	// 用一个只模仿协议的测试子进程代替实际实现。
	executable := filepath.Join(t.TempDir(), "minirunc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "go", "build", "-o", executable, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build minirunc: %v\n%s", err, output)
	}

	valid := &initConfig{
		RootfsPath: t.TempDir(),
		Spec: &specs.Spec{
			Version: "1.3.0",
			Root:    &specs.Root{Path: "rootfs"},
			Process: &specs.Process{Args: []string{"/bin/sh"}, Cwd: "/"},
		},
	}
	largeSpec := *valid.Spec
	largeProcess := *valid.Spec.Process
	largeProcess.Env = []string{"PAYLOAD=" + strings.Repeat("x", 256*1024)}
	largeSpec.Process = &largeProcess

	for _, test := range []struct {
		name   string
		config *initConfig
		want   string
	}{
		{"valid configuration", valid, ""},
		{"large configuration", &initConfig{RootfsPath: valid.RootfsPath, Spec: &largeSpec}, ""},
		{"missing spec", &initConfig{RootfsPath: valid.RootfsPath}, "OCI spec is required"},
		{"relative rootfs", &initConfig{RootfsPath: "relative", Spec: valid.Spec}, "rootfsPath"},
		{"invalid spec", &initConfig{RootfsPath: valid.RootfsPath, Spec: &specs.Spec{}}, "ociVersion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 如果任何一方忘记关闭写端，超时会终止子进程，让测试报错，
			// 而不是永久等待 EOF。大消息还验证双方可以并发读写管道。
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := startInit(ctx, executable, test.config)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStartInitRejectsMissingResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// /bin/true 只会成功退出，不返回控制消息。退出码 0 不能代替确认消息。
	if err := startInit(ctx, "/bin/true", nil); err == nil {
		t.Fatal("child exit without a result was accepted")
	}
}

func TestStartInitCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := startInit(ctx, "/proc/self/exe", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestHandleInitIgnoresNormalCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"validate"}, {"--help"}, {initCommand, "extra"}} {
		if handled, _ := HandleInit(args); handled {
			t.Fatalf("normal invocation was handled as init: %v", args)
		}
	}
}
