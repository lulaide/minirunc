package spec

import (
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestParseCapabilities(t *testing.T) {
	for _, config := range []*specs.LinuxCapabilities{nil, {}} {
		got, err := ParseCapabilities(config)
		if err != nil || got != (CapabilitySets{}) {
			t.Fatalf("empty capabilities = %+v, error %v", got, err)
		}
	}
	names := []string{"CAP_CHOWN", "CAP_SETPCAP", "CAP_NET_BIND_SERVICE", "CAP_PERFMON", "CAP_BPF", "CAP_CHECKPOINT_RESTORE"}
	got, err := ParseCapabilities(&specs.LinuxCapabilities{
		Bounding: names, Permitted: names, Effective: names, Inheritable: names, Ambient: names,
	})
	want := uint64(1<<0 | 1<<8 | 1<<10 | 1<<38 | 1<<39 | 1<<40)
	if err != nil || got != (CapabilitySets{want, want, want, want, want}) {
		t.Fatalf("capabilities = %+v, error %v, want mask %#x", got, err, want)
	}
	// bounding 约束 exec 时从文件获得的能力，不要求 permitted/inheritable 是它的子集。
	if _, err := ParseCapabilities(&specs.LinuxCapabilities{Permitted: []string{"CAP_CHOWN"}, Inheritable: []string{"CAP_CHOWN"}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecRejectsInvalidCapabilities(t *testing.T) {
	for _, test := range []struct {
		caps  specs.LinuxCapabilities
		field string
	}{
		{specs.LinuxCapabilities{Bounding: []string{"CAP_UNKNOWN"}}, "bounding[0]"},
		{specs.LinuxCapabilities{Permitted: []string{"cap_chown"}}, "permitted[0]"},
		{specs.LinuxCapabilities{Inheritable: []string{"CAP_CHOWN", "CAP_CHOWN"}}, "inheritable[1]"},
		{specs.LinuxCapabilities{Effective: []string{"CAP_CHOWN"}}, "effective: must be a subset"},
		{specs.LinuxCapabilities{Ambient: []string{"CAP_CHOWN"}, Permitted: []string{"CAP_CHOWN"}}, "ambient: must be a subset"},
		{specs.LinuxCapabilities{Ambient: []string{"CAP_CHOWN"}, Inheritable: []string{"CAP_CHOWN"}}, "ambient: must be a subset"},
	} {
		config := baselineSpec()
		config.Process.Capabilities = &test.caps
		if err := ValidateSpec(config); err == nil || !strings.Contains(err.Error(), test.field) {
			t.Fatalf("error = %v, want %q", err, test.field)
		}
	}
}
