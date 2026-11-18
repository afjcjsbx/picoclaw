package shellguard

import "testing"

func TestBraceExpand(t *testing.T) {
	tests := []struct {
		in, want string
		changed  bool
	}{
		{`{rm,-rf,/}`, `rm -rf /`, true},
		{`rm -rf {/etc,/tmp}`, `rm -rf /etc /tmp`, true},
		{`echo {a,b} x`, `echo a b x`, true},
		{`echo x{a,b}`, `echo x a b`, true},
		{`{a,b}{c,d}`, `a b c d`, true},
		{`echo {a,{b,c}}`, `echo a b c`, true},
		{`echo '{a,b}'`, ``, false},
		{`echo "{a,b}"`, ``, false},
		{`echo {not,a,brace`, ``, false},
		{`echo plain`, ``, false},
	}
	for _, tc := range tests {
		got, changed := BraceExpand(tc.in)
		if changed != tc.changed || got != tc.want {
			t.Errorf("BraceExpand(%q) = %q, %v; want %q, %v", tc.in, got, changed, tc.want, tc.changed)
		}
	}
}

func TestBraceExpandBounds(t *testing.T) {
	// A group with too many alternatives is left alone rather than expanded
	// into unbounded work.
	in := "echo {" + "a," + "b," + "c," + "d," + "e," + "f," + "g," + "h," + "i," + "j," + "k," + "l," + "m," + "n," + "o," + "p," + "q," + "r," + "s," + "t," + "u," + "v," + "w," + "x," + "y," + "z," + "aa," + "bb," + "cc," + "dd," + "ee," + "ff," + "gg," + "hh," + "ii," + "jj," + "kk," + "ll," + "mm," + "nn," + "oo," + "pp," + "qq," + "rr," + "ss," + "tt," + "uu," + "vv," + "ww," + "xx," + "yy," + "zz," + "a1," + "b1," + "c1," + "d1," + "e1," + "f1," + "g1," + "h1," + "i1," + "j1," + "k1," + "l1," + "m1," + "n1," + "o1," + "p1," + "q1," + "r1," + "s1," + "t1," + "u1," + "v1," + "w1," + "x1," + "y1," + "z1}"
	if got, changed := BraceExpand(in); changed {
		t.Fatalf("expected no expansion, got %q", got)
	}
}
