package fstools

import "testing"

func TestHasPathPrefixFold(t *testing.T) {
	unixSep := func(b byte) bool { return b == '/' }
	tests := []struct {
		name   string
		path   string
		prefix string
		want   bool
	}{
		{name: "exact", path: "/home/u/.picoclaw", prefix: "/home/u/.picoclaw", want: true},
		{name: "child", path: "/home/u/.picoclaw/auth.json", prefix: "/home/u/.picoclaw", want: true},
		{name: "sibling prefix", path: "/home/u/.picoclaw-evil", prefix: "/home/u/.picoclaw", want: false},
		{name: "case insensitive", path: "/ETC/PASSWD", prefix: "/etc", want: true},
		{name: "filesystem root", path: "/etc/passwd", prefix: "/", want: true},
		{name: "root exact", path: "/", prefix: "/", want: true},
		{name: "empty path", path: "", prefix: "/", want: false},
		{name: "shorter path", path: "/home", prefix: "/home/u", want: false},
		{name: "empty prefix", path: "", prefix: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasPathPrefixFoldSep(tt.path, tt.prefix, unixSep); got != tt.want {
				t.Errorf("hasPathPrefixFoldSep(%q, %q) = %v, want %v", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestHasPathPrefixFoldWindowsRoots(t *testing.T) {
	windowsSep := func(b byte) bool { return b == '\\' || b == '/' }
	tests := []struct {
		name   string
		path   string
		prefix string
		want   bool
	}{
		{name: "drive root", path: `C:\auth.json`, prefix: `C:\`, want: true},
		{name: "drive root exact", path: `C:\`, prefix: `C:\`, want: true},
		{name: "drive root case insensitive", path: `c:\AUTH.JSON`, prefix: `C:\`, want: true},
		{name: "other drive", path: `D:\auth.json`, prefix: `C:\`, want: false},
		{name: "unc root", path: `\\server\share\auth.json`, prefix: `\\server\share\`, want: true},
		{name: "unc root exact", path: `\\server\share\`, prefix: `\\server\share\`, want: true},
		{name: "unc sibling", path: `\\server\share2\auth.json`, prefix: `\\server\share\`, want: false},
		{name: "normal dir", path: `C:\Users\u\auth.json`, prefix: `C:\Users\u`, want: true},
		{name: "normal dir sibling", path: `C:\Users\u2\auth.json`, prefix: `C:\Users\u`, want: false},
		{name: "forward separators", path: `C:/auth.json`, prefix: `C:/`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasPathPrefixFoldSep(tt.path, tt.prefix, windowsSep); got != tt.want {
				t.Errorf("hasPathPrefixFoldSep(%q, %q) = %v, want %v", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}
