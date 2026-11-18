package shellguard

import (
	"strings"
	"testing"
)

func interpreterTestPolicy() WorkspacePolicy {
	return WorkspacePolicy{
		Cwd:  "/work/project",
		Home: "/home/user",
		AllowPath: func(p string) bool {
			return workspacePathWithin("/work/project", p)
		},
	}
}

func checkInterpreter(t *testing.T, command string) error {
	t.Helper()
	file, err := Parse(command)
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	return CheckInterpreterFile(command, file, interpreterTestPolicy(), 0)
}

func TestInterpreterAllowsPureComputationAndWorkspacePaths(t *testing.T) {
	allowed := []string{
		`python3 -c "print(1+1)"`,
		`python3 -c "import math; print(math.sqrt(4))"`,
		`node -e "console.log(1)"`,
		`ruby -e "puts 1"`,
		`php -r "echo 1;"`,
		`awk '{print $1}' notes.txt`,
		`sed -n '1p' notes.txt`,
		`python3 -c "open('out.txt','w').write('x')"`,
		`node -e "require('fs').writeFileSync('build/out.txt','x')"`,
		`python3 -c "print('/etc/passwd')"`, // a literal in a message, not an access
	}
	for _, command := range allowed {
		if err := checkInterpreter(t, command); err != nil {
			t.Errorf("expected allowed, got %v: %s", err, command)
		}
	}
}

func TestInterpreterBlocksOperationsOnOutsidePaths(t *testing.T) {
	blocked := []string{
		`python3 -c "import shutil; shutil.rmtree('/tmp/canary')"`,
		`python3 -c "open('/tmp/out.txt','w').write('x')"`,
		`python3 -c "print(open('/etc/passwd').read())"`,
		`python3 -c "open('../outside.txt','w').write('x')"`,
		`python3 -c "open('$HOME/outside.txt','w').write('x')"`,
		`python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" /tmp/canary`,
		`perl -e 'use File::Path qw(remove_tree); remove_tree("/tmp/canary")'`,
		`node -e "require('fs').rmSync('/tmp/canary',{recursive:true,force:true})"`,
		`ruby -e 'require "fileutils"; FileUtils.rm_rf("/tmp/canary")'`,
		`php -r "unlink('/tmp/canary');"`,
		`lua -e 'os.remove("/tmp/canary")'`,
		`Rscript -e 'unlink("/tmp/canary", recursive=TRUE)'`,
		`awk 'BEGIN{system("rm -rf /tmp/canary")}'`,
		`sed -e '1e rm -rf /tmp/canary' /dev/null`,
		`osascript -e 'do shell script "rm -rf /tmp/canary"'`,
		`bash -c 'rm -rf /tmp/canary'`,
		`sh -c 'echo x > /tmp/canary'`,
	}
	for _, command := range blocked {
		err := checkInterpreter(t, command)
		if err == nil {
			t.Errorf("expected blocked: %s", command)
			continue
		}
		if !strings.Contains(err.Error(), "outside the workspace") {
			t.Errorf("unexpected error for %s: %v", command, err)
		}
	}
}

func TestInterpreterAllowsInsideArgumentPaths(t *testing.T) {
	// The path is supplied as a separate argv word, not inside the quoted
	// program. It is inside the workspace, so the operation is allowed.
	command := `python3 -c "import os,sys; os.remove(sys.argv[1])" build/out.txt`
	if err := checkInterpreter(t, command); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
}

func TestInterpreterIgnoresNonInterpreters(t *testing.T) {
	for _, command := range []string{
		`echo python3 -c "shutil.rmtree('/tmp/x')"`,
		`grep -r rmtree .`,
	} {
		if err := checkInterpreter(t, command); err != nil {
			t.Errorf("expected allowed, got %v: %s", err, command)
		}
	}
}
