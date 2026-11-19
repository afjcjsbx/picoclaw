package shellguard

import "testing"

func TestUnescapeUnquotedLit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "escaped letter", in: `r\m`, want: `rm`},
		{name: "escaped letters", in: `\r\m`, want: `rm`},
		{name: "double backslash keeps one", in: `r\\m`, want: `r\m`},
		{name: "triple backslash keeps one", in: `r\\\m`, want: `r\m`},
		{name: "escaped space", in: `r\ m`, want: `r m`},
		{name: "line continuation", in: "r\\\nm", want: "rm"},
		{name: "crlf continuation", in: "r\\\r\nm", want: "rm"},
		{name: "trailing backslash preserved", in: `r\`, want: `r\`},
		{name: "no escapes", in: `plain`, want: `plain`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := unescapeUnquotedLit(tt.in); got != tt.want {
				t.Fatalf("unescapeUnquotedLit(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnescapeDoubleQuotedLit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ordinary letter keeps backslash", in: `r\m`, want: `r\m`},
		{name: "double backslash keeps one", in: `r\\m`, want: `r\m`},
		{name: "escaped quote", in: `r\"m`, want: `r"m`},
		{name: "escaped dollar", in: `r\$m`, want: `r$m`},
		{name: "escaped backtick", in: "r\\`m", want: "r`m"},
		{name: "line continuation", in: "r\\\nm", want: "rm"},
		{name: "no escapes", in: `plain`, want: `plain`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := unescapeDoubleQuotedLit(tt.in); got != tt.want {
				t.Fatalf("unescapeDoubleQuotedLit(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodeANSIValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "hex escape", in: `r\x6d`, want: `rm`, ok: true},
		{name: "unknown escape keeps backslash", in: `r\m`, want: `r\m`, ok: true},
		{name: "escaped backslash is literal", in: `r\x5cm`, want: `r\m`, ok: true},
		{name: "double backslash is literal", in: `r\\m`, want: `r\m`, ok: true},
		{name: "unterminated escape", in: `r\`, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := decodeANSIValue(tt.in)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Fatalf("decodeANSIValue(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
