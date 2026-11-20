package shellguard

import (
	"slices"
	"testing"
)

func assessCommand(t *testing.T, g *Guard, command string) Assessment {
	t.Helper()
	file, err := Parse(command)
	if err != nil {
		t.Fatalf("Parse(%q): %v", command, err)
	}
	return g.AssessFile(command, file)
}

// TestConstResolution checks that a constant the script assigned earlier is
// classified as the command it expands to.
func TestConstResolution(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "medium")
	tests := []struct {
		command string
		want    RiskLevel
	}{
		{`R=rm; $R -rf x`, RiskCritical},
		{`R=rm; ${R} -rf x`, RiskCritical},
		{`R="rm"; "$R" -rf x`, RiskCritical},
		{`R=r; S=m; "$R$S" -rf x`, RiskCritical},
		{`A=rm; B=$A; $B -rf x`, RiskCritical},
		{`export R=rm; $R -rf x`, RiskCritical},
		{`R=rm && $R -rf x`, RiskCritical},
		{`R=rm; true && $R -rf x`, RiskCritical},
		{`R=rm; echo hi | $R -rf x`, RiskCritical},
		{`S='rm -rf x'; sh -c "$S"`, RiskCritical},
		{`sh -c 'R=rm; $R -rf x'`, RiskCritical},
		{`R=sudo; $R ls`, RiskCritical},
		{`R=rm; unset R; R=rm; $R -rf x`, RiskCritical},
		{`R=ls; R=rm; $R -rf x`, RiskCritical},
		{`R=rm; ls; $R -rf x`, RiskCritical},
		{`R=rm; cd /tmp; $R -rf x`, RiskCritical},
		{`D=build; ls $D`, RiskLow},
		{`R=ls; $R -la`, RiskLow},
		{`set -- ls -la; "$@"`, RiskLow},
		{`set -- sudo ls; "$@"`, RiskCritical},
		{`set -- ls; "$@" -la`, RiskLow},
	}
	for _, tt := range tests {
		a := assessCommand(t, g, tt.command)
		if a.Verdict != VerdictKnown || a.Level != tt.want {
			t.Errorf("%q = verdict %d level %v (%s), want known %v",
				tt.command, a.Verdict, a.Level, a.Reason, tt.want)
		}
	}
}

// TestConstResolutionRejectsExpandedDestructiveOperands: a value the script
// assigned reaches the risk classifier, but no path guard has seen it, so a
// destructive command keeps being rejected when an operand came from an
// expansion, as it was before constants resolved.
func TestConstResolutionRejectsExpandedDestructiveOperands(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "critical")
	for _, command := range []string{
		`R=rm F=-rf; $R $F x`,
		`F=a.txt; rm $F`,
		`set -- rm -rf; "$@" x`,
		`set -- rm -rf; $@ x`,
		`set -- rm -rf x; "$@"`,
		`set -- rm -rf z; "$1" "$2" "$3"`,
		`set -- x rm -rf; shift; "$@" y`,
		`set -e; set -- rm -rf; "$@" x`,
	} {
		a := assessCommand(t, g, command)
		if a.Verdict != VerdictReject {
			t.Errorf("%q = verdict %d (%s), want reject", command, a.Verdict, a.Reason)
		}
	}
}

// TestConstResolutionStopsWhenUnsafe covers the constructs that could change a
// variable without the straight-line pass seeing it. None may end as an allowed
// command: the strict test policy rejects anything that is not known and low.
func TestConstResolutionStopsWhenUnsafe(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "low")
	for _, command := range []string{
		`R=ls; f() { R=rm; }; f; $R -rf x`,
		`R=ls; eval 'R=rm'; $R -rf x`,
		`R=ls; . ./set-r.sh; $R -rf x`,
		`R=ls; read R; $R -rf x`,
		`R=ls; printf -v R rm; $R -rf x`,
		`R=ls; trap 'R=rm' DEBUG; $R -rf x`,
		`R=ls; if true; then R=rm; fi; $R -rf x`,
		`R=ls; for R in rm; do :; done; $R -rf x`,
		`R=ls; { R=rm; }; $R -rf x`,
		`R=ls; (R=rm); $R -rf x`,
		`R=ls; true && R=rm; $R -rf x`,
		`R=rm; false && R=ls; $R -rf x`,
		`R=rm; true || R=ls; $R -rf x`,
		`R=rm; R=ls & $R -rf x`,
		`R=rm; echo | R=ls; $R -rf x`,
		`R=ls; R=rm :; $R -rf x`,
		`set -- rm -rf; false && set -- ls; "$@" x`,
		`R=ls; false || R=rm; $R -rf x`,
		`R=ls; R=rm & $R -rf x`,
		`R=ls; echo | R=rm; $R -rf x`,
		`R=ls; R=rm env true; $R -rf x`,
		`R=; echo ${R:=rm}; "$R" -rf x`,
		`R=; echo > ${R:=rm}; "$R" -rf x`,
		`R=ls; echo ${R:=rm}; $R -rf x`,
		`IFS=r; R=rm; $R -rf x`,
		`R=ls; declare -n R=T; T=rm; $R -rf x`,
		`T=ls; declare -n R=T; R=rm; $T -rf x`,
		`R=rm; readonly R; R=ls; $R -rf x`,
		`R=ls; export R=rm; read X; $R -rf x`,
		`R=rm; unset R; $R -rf x`,
		`R=rm; unset -f R; $R -rf x`,
		`set -- ls; true && set -- rm -rf; "$@" x`,
		`set -- ls; set -e; "$@" x`,
		`set -- x ls; shift 5; "$@" y`,
		`set -- rm -rf; set -- $(echo ls); "$@" x`,
		`R='rm -rf'; $R x`,
		`R='rm -rf'; "$R" x`,
		`R='r*'; $R -rf x`,
		`R=; $R rm -rf x`,
		`R=rm; "$RR" -rf x`,
		`R=rm; $R2 -rf x`,
		`R=$(echo ls); $R -la`,
		`R=rm; R+=x; $R -rf x`,
		`R=rm; R[1]=x; $R -rf x`,
		`R=~; $R -rf x`,
		`PWD=ls; $PWD -la`,
		`set -- 'rm -rf' x; $@`,
		`$0 -la`,
		`"$@" -la`,
	} {
		if blocked := checkGuard(g, command); blocked == nil {
			t.Errorf("Check(%q) allowed, want blocked", command)
		}
	}
}

// TestConstResolutionKeepsRmOperandsDynamic covers values that the shell would
// split or re-read differently from the single word the guard sees. At medium
// threshold a wrong resolution would let a destructive rm through.
func TestConstResolutionKeepsRmOperandsDynamic(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "medium")
	for _, command := range []string{
		`IFS=:; A=--recursive:--force; rm $A x`,
		`A='--recursive --force'; rm $A x`,
		`A='--recursive --force'; rm $A`,
	} {
		if blocked := checkGuard(g, command); blocked == nil {
			t.Errorf("Check(%q) allowed, want blocked", command)
		}
	}
}

// TestConstResolutionKeepsLiteralWords makes sure resolution does not change
// the commands it cannot follow: they stay dynamic, as before.
func TestConstResolutionKeepsLiteralWords(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "medium")
	for _, command := range []string{
		`$R -rf x`,
		`"$@" x`,
		`R=rm; f() { :; }; $R -rf x`,
		`if true; then R=rm; $R -rf x; fi`,
		`R=rm; if true; then :; fi; $R -rf x`,
	} {
		a := assessCommand(t, g, command)
		if a.Verdict != VerdictUnknown {
			t.Errorf("%q = verdict %d, want unknown", command, a.Verdict)
		}
	}
}

func TestCanonicalFormsResolveConstants(t *testing.T) {
	t.Parallel()

	for command, want := range map[string]string{
		`R=rm; $R -rf /`:           "rm -rf /",
		`set -- rm -rf; "$@" /`:    "rm -rf /",
		`S='rm -rf /'; sh -c "$S"`: "rm -rf /",
		`R=rm && "$R" -rf /`:       "rm -rf /",
	} {
		if forms := CanonicalCommands(command); !slices.Contains(forms, want) {
			t.Errorf("CanonicalCommands(%q) = %q, want %q", command, forms, want)
		}
	}

	// A function may have changed R, so nothing resolves.
	for _, form := range CanonicalCommands(`R=ls; f() { R=rm; }; f; $R -rf /`) {
		if form == "ls -rf /" || form == "rm -rf /" {
			t.Errorf("resolved %q across a function call", form)
		}
	}
}

func TestUnlinkIsHighRisk(t *testing.T) {
	t.Parallel()

	g := mustGuard(t, "medium")
	a := assessCommand(t, g, "unlink f")
	if a.Verdict != VerdictKnown || a.Level != RiskHigh {
		t.Fatalf("unlink = verdict %d level %v, want known high", a.Verdict, a.Level)
	}
}
