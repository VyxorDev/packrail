package install

import (
	"reflect"
	"testing"
)

func TestLaunchArgs(t *testing.T) {
	entry := "/tmp/tool/app.jar"
	cases := []struct {
		name       string
		configured []string
		hasRuntime bool
		want       []string
	}{
		{
			name:       "default runtime entrypoint",
			hasRuntime: true,
			want:       []string{entry},
		},
		{
			name:       "java jar",
			configured: []string{"-jar", "{entrypoint}"},
			hasRuntime: true,
			want:       []string{"-jar", entry},
		},
		{
			name:       "python explicit entrypoint",
			configured: []string{"{entrypoint}"},
			hasRuntime: true,
			want:       []string{entry},
		},
		{
			name:       "runtime args append entrypoint when placeholder omitted",
			configured: []string{"--flag"},
			hasRuntime: true,
			want:       []string{"--flag", entry},
		},
		{
			name:       "native has no implicit entry argument",
			configured: nil,
			hasRuntime: false,
			want:       nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := launchArgs(tc.configured, entry, tc.hasRuntime)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("launchArgs() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
