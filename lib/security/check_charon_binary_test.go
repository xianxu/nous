package security

import "testing"

func TestExtractCodesignField(t *testing.T) {
	out := `Executable=/Users/foo/bin/charon
Identifier=com.charon.cli
Format=Mach-O thin (arm64)
Authority=Developer ID Application: Test (ABCDEFG123)
TeamIdentifier=ABCDEFG123
`
	cases := []struct {
		field string
		want  string
	}{
		{"Identifier", "com.charon.cli"},
		{"TeamIdentifier", "ABCDEFG123"},
		{"Format", "Mach-O thin (arm64)"},
		{"NotPresent", ""},
	}
	for _, tc := range cases {
		if got := extractCodesignField(out, tc.field); got != tc.want {
			t.Errorf("extractCodesignField(%q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestCodesignHasRuntimeFlag(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "with runtime",
			out:  "CodeDirectory v=20500 size=8888 flags=0x10000(runtime) hashes=2+3 location=embedded",
			want: true,
		},
		{
			name: "without runtime — adhoc only",
			out:  "CodeDirectory v=20400 size=4444 flags=0x2(adhoc) hashes=2+3 location=embedded",
			want: false,
		},
		{
			name: "no flags at all",
			out:  "CodeDirectory v=20100 size=2222 hashes=2+3 location=embedded",
			want: false,
		},
		{
			name: "runtime + library validation combined",
			out:  "CodeDirectory v=20500 size=8888 flags=0x12000(runtime,library-validation) hashes=2+3",
			want: true,
		},
		{
			name: "no CodeDirectory line",
			out:  "Identifier=com.charon.cli\nAuthority=...",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codesignHasRuntimeFlag(tc.out); got != tc.want {
				t.Errorf("codesignHasRuntimeFlag = %v, want %v", got, tc.want)
			}
		})
	}
}
