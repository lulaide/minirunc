package cgroup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func pointer[T any](value T) *T { return &value }

func TestResourceSettings(t *testing.T) {
	for _, test := range []struct {
		name      string
		resources *specs.LinuxResources
		want      []limitSetting
	}{
		{"absent", nil, nil},
		{"empty", &specs.LinuxResources{Memory: &specs.LinuxMemory{}, CPU: &specs.LinuxCPU{}, Pids: &specs.LinuxPids{}}, nil},
		{"finite", &specs.LinuxResources{
			Memory: &specs.LinuxMemory{Limit: pointer(int64(67108864))},
			CPU:    &specs.LinuxCPU{Quota: pointer(int64(50000)), Period: pointer(uint64(100000))},
			Pids:   &specs.LinuxPids{Limit: pointer(int64(64))},
		}, []limitSetting{{"memory.max", "67108864"}, {"cpu.max", "50000 100000"}, {"pids.max", "64"}}},
		{"unlimited", &specs.LinuxResources{
			Memory: &specs.LinuxMemory{Limit: pointer(int64(-1))},
			CPU:    &specs.LinuxCPU{Quota: pointer(int64(-1))},
			Pids:   &specs.LinuxPids{Limit: pointer(int64(-1))},
		}, []limitSetting{{"memory.max", "max"}, {"cpu.max", "max 100000"}, {"pids.max", "max"}}},
		{"zero limits", &specs.LinuxResources{Memory: &specs.LinuxMemory{Limit: pointer(int64(0))}, Pids: &specs.LinuxPids{Limit: pointer(int64(0))}}, []limitSetting{{"memory.max", "0"}, {"pids.max", "0"}}},
		{"quota only", &specs.LinuxResources{CPU: &specs.LinuxCPU{Quota: pointer(int64(20000))}}, []limitSetting{{"cpu.max", "20000 100000"}}},
		{"period only", &specs.LinuxResources{CPU: &specs.LinuxCPU{Period: pointer(uint64(200000))}}, []limitSetting{{"cpu.max", "max 200000"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resourceSettings(test.resources)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("settings = %v, error = %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestResourceSettingsRejectInvalidOrUnsupportedConfiguration(t *testing.T) {
	for _, test := range []struct {
		resources specs.LinuxResources
		field     string
	}{
		{specs.LinuxResources{Memory: &specs.LinuxMemory{Limit: pointer(int64(-2))}}, "memory.limit"},
		{specs.LinuxResources{Pids: &specs.LinuxPids{Limit: pointer(int64(-2))}}, "pids.limit"},
		{specs.LinuxResources{CPU: &specs.LinuxCPU{Quota: pointer(int64(-2))}}, "cpu.quota"},
		{specs.LinuxResources{CPU: &specs.LinuxCPU{Quota: pointer(int64(0))}}, "cpu.quota"},
		{specs.LinuxResources{CPU: &specs.LinuxCPU{Period: pointer(uint64(0))}}, "cpu.period"},
		{specs.LinuxResources{Memory: &specs.LinuxMemory{Swap: pointer(int64(-1))}}, "memory"},
		{specs.LinuxResources{CPU: &specs.LinuxCPU{Shares: pointer(uint64(1024))}}, "cpu"},
		{specs.LinuxResources{Unified: map[string]string{"cgroup.procs": "1"}}, "linux.resources"},
		{specs.LinuxResources{Devices: []specs.LinuxDeviceCgroup{{Allow: false}}}, "linux.resources"},
	} {
		_, err := resourceSettings(&test.resources)
		if err == nil || !strings.Contains(err.Error(), test.field) {
			t.Fatalf("error = %v, want %q", err, test.field)
		}
	}
}

func TestApplyLimitsRejectsOrdinaryDirectory(t *testing.T) {
	dir := t.TempDir()
	resources := &specs.LinuxResources{Pids: &specs.LinuxPids{Limit: pointer(int64(32))}}
	if err := ApplyLimits(dir, resources); err == nil || !strings.Contains(err.Error(), "cgroup v2 filesystem") {
		t.Fatalf("ordinary directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pids.max")); !os.IsNotExist(err) {
		t.Fatalf("created a fake control file: %v", err)
	}
}

func TestApplyLimitsOnCgroupV2(t *testing.T) {
	if os.Getenv("MINIRUNC_PRIVILEGED_TESTS") != "1" {
		t.Skip("set MINIRUNC_PRIVILEGED_TESTS=1 to test cgroup v2 writes")
	}
	parent := os.Getenv("MINIRUNC_CGROUP_TEST_PARENT")
	if parent == "" {
		t.Fatal("MINIRUNC_CGROUP_TEST_PARENT must name a writable parent with cpu, memory and pids enabled")
	}
	// 只在指定父目录下创建一个空测试 cgroup，不迁移进程，不改父级控制器或限制。
	dir, err := os.MkdirTemp(parent, "minirunc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(dir); err != nil {
			t.Errorf("remove test cgroup: %v", err)
		}
	})
	check := func(file, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("%s = %q, error %v, want %q", file, data, err, want)
		}
	}
	resources := &specs.LinuxResources{
		Memory: &specs.LinuxMemory{Limit: pointer(int64(67108864))},
		CPU:    &specs.LinuxCPU{Quota: pointer(int64(50000)), Period: pointer(uint64(100000))},
		Pids:   &specs.LinuxPids{Limit: pointer(int64(32))},
	}
	if err := ApplyLimits(dir, resources); err != nil {
		t.Fatal(err)
	}
	check("memory.max", "67108864")
	check("cpu.max", "50000 100000")
	check("pids.max", "32")
	// 校验完整配置后才写文件：后面的 CPU 参数无效，前面的内存限制也不能改动。
	resources.Memory.Limit = pointer(int64(33554432))
	resources.CPU.Period = pointer(uint64(0))
	if err := ApplyLimits(dir, resources); err == nil {
		t.Fatal("accepted invalid CPU period")
	}
	check("memory.max", "67108864")
	if err := ApplyLimits(dir, &specs.LinuxResources{
		Memory: &specs.LinuxMemory{Limit: pointer(int64(-1))},
		CPU:    &specs.LinuxCPU{Quota: pointer(int64(-1))},
		Pids:   &specs.LinuxPids{Limit: pointer(int64(-1))},
	}); err != nil {
		t.Fatal(err)
	}
	check("memory.max", "max")
	check("cpu.max", "max 100000")
	check("pids.max", "max")
}
