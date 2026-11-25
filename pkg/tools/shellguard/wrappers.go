package shellguard

import "strings"

// Wrapper commands run another command (or a command string) after their own
// options. wrapperSpecs says how many words each one consumes before its
// target; resolveLaunch uses it to find what a wrapper launches.

type wrapperSpec struct {
	valueFlags     map[string]bool
	commandFlags   map[string]bool
	skipPositional int
	envArgs        bool
	// shellAfterDoubleDash marks wrappers that execute /bin/bash with the
	// arguments after `--` (capsh). Those arguments are parsed as a script.
	shellAfterDoubleDash bool
}

// containerCLISpec is shared by lxc and incus: both take a subcommand, a
// container name and then the command to run inside the container.
var containerCLISpec = wrapperSpec{
	valueFlags: map[string]bool{
		"--project": true, "-P": true, "--remote": true,
		"--env": true, "--cwd": true, "--user": true,
		"--group": true, "--mode": true, "--width": true,
		"--height": true,
	},
	skipPositional: 2,
}

var wrapperSpecs = map[string]wrapperSpec{
	"env":     {envArgs: true},
	"command": {},
	"builtin": {},
	"nohup":   {},
	"time": {
		valueFlags: map[string]bool{
			"-o": true, "--output": true, "-f": true, "--format": true,
		},
	},
	"nice": {
		valueFlags: map[string]bool{"-n": true, "--adjustment": true},
	},
	"ionice": {
		valueFlags: map[string]bool{
			"-c": true, "-n": true, "-p": true,
			"--class": true, "--classdata": true, "--pid": true,
		},
	},
	"stdbuf": {
		valueFlags: map[string]bool{
			"-i": true, "-o": true, "-e": true,
			"--input": true, "--output": true, "--error": true,
		},
	},
	"timeout": {
		valueFlags: map[string]bool{
			"-s": true, "-k": true, "--signal": true, "--kill-after": true,
		},
		skipPositional: 1,
	},
	"watch": {
		valueFlags: map[string]bool{"-n": true, "--interval": true},
	},
	"setsid": {},
	// The first positional is the applet itself, so it is the nested command
	// name rather than an argument to skip.
	"busybox": {},
	"xvfb-run": {
		valueFlags: map[string]bool{
			"-e": true, "-n": true, "-s": true,
			"--error-file": true, "--server-num": true, "--server-args": true,
		},
	},
	"xargs": {
		valueFlags: map[string]bool{
			"-a": true, "-d": true, "-E": true, "-e": true, "-I": true,
			"-i": true, "-L": true, "-l": true, "-n": true, "-P": true,
			"-s": true, "--arg-file": true, "--delimiter": true,
			"--max-args": true, "--max-procs": true, "--max-chars": true,
			"--replace": true,
		},
	},
	"runuser": {
		valueFlags: map[string]bool{
			"-u": true, "--user": true, "-g": true, "--group": true,
			"-G": true, "--supp-group": true, "-s": true, "--shell": true,
			"-w": true, "--whitelist-environment": true,
		},
		commandFlags: map[string]bool{
			"-c": true, "--command": true, "--session-command": true,
		},
	},
	"setpriv": {
		valueFlags: map[string]bool{
			"--reuid": true, "--regid": true, "--groups": true,
			"--inh-caps": true, "--ambient-caps": true,
			"--bounding-set": true, "--securebits": true,
			"--selinux-label": true, "--apparmor-profile": true,
			"--landlock-access": true, "--landlock-rule": true,
		},
	},
	"capsh": {
		valueFlags: map[string]bool{
			"--user": true, "--group": true, "--uid": true, "--gid": true,
			"--shell": true, "--keep": true, "--caps": true, "--inh": true,
			"--addamb": true, "--delamb": true, "--drop": true,
			"--chroot": true, "--securebits": true,
		},
		shellAfterDoubleDash: true,
	},
	"machinectl": {
		valueFlags: map[string]bool{
			"-M": true, "--machine": true, "--uid": true, "--host": true,
			"-H": true, "-i": true, "--identity": true, "-p": true,
			"--property": true, "-n": true, "--lines": true, "-o": true,
			"--output": true, "-s": true, "--signal": true,
			"--kill-whom": true,
		},
		skipPositional: 2,
	},
	"lxc":   containerCLISpec,
	"incus": containerCLISpec,
	"flock": {
		valueFlags: map[string]bool{
			"-w": true, "--wait": true, "--timeout": true,
			"-E": true, "--conflict-exit-code": true,
		},
		commandFlags:   map[string]bool{"-c": true, "--command": true},
		skipPositional: 1,
	},
	"chrt": {
		valueFlags: map[string]bool{
			"-T": true, "--sched-runtime": true,
			"-P": true, "--sched-period": true,
			"-D": true, "--sched-deadline": true,
		},
		skipPositional: 1,
	},
	"taskset": {skipPositional: 1},
}

func wrapperNested(words []word, spec wrapperSpec) ([]word, bool, bool) {
	positional := spec.skipPositional
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.dynamic {
			return nil, false, true
		}

		text := w.text
		if text == "--" {
			// For wrappers that consume positional arguments (timeout, flock,
			// lxc exec), "--" only ends option parsing: the command still comes
			// after those positionals. Returning here would treat the next
			// positional (a duration, lock file or container) as the command.
			if positional > 0 {
				continue
			}
			if i+1 < len(words) {
				return words[i+1:], true, false
			}
			return nil, false, false
		}
		if strings.HasPrefix(text, "-") && text != "-" {
			if spec.valueFlags[text] {
				i++
				continue
			}
			if name, _, ok := strings.Cut(text, "="); ok && spec.valueFlags[name] {
				continue
			}
			if len(text) > 2 && text[1] != '-' && spec.valueFlags[text[:2]] {
				continue
			}
			continue
		}
		if positional > 0 {
			positional--
			continue
		}
		return words[i:], true, false
	}
	return nil, false, false
}

// argsAfterDoubleDash returns the arguments after the first literal "--".
func argsAfterDoubleDash(words []word) ([]word, bool) {
	for i, w := range words {
		if !w.dynamic && w.text == "--" {
			return words[i+1:], true
		}
	}
	return nil, false
}

// commandFlagValue finds a flag whose value is a shell command string (-c for
// runuser, flock and similar wrappers). The third result reports a dynamic word
// before the flag: the effective flag cannot be resolved, so the caller must
// stay fail-closed. After "--" the value is a positional argument, not a flag.
func commandFlagValue(words []word, flags map[string]bool) (word, bool, bool) {
	if len(flags) == 0 {
		return word{}, false, false
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.text == "--" {
			return word{}, false, false
		}
		if w.dynamic {
			return word{}, false, true
		}
		text := w.text
		if flags[text] {
			if i+1 < len(words) {
				return words[i+1], true, false
			}
			return word{}, false, false
		}
		if name, value, ok := strings.Cut(text, "="); ok && flags[name] {
			return word{text: value}, true, false
		}
		if strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && len(text) > 2 {
			for flag := range flags {
				if len(flag) == 2 && strings.HasPrefix(text, flag) {
					return word{text: text[2:]}, true, false
				}
			}
		}
	}
	return word{}, false, false
}

// hasCommandLookupFlag reports whether command(1) was invoked with -v/-V,
// including combined forms such as -pv. Lookup mode does not execute the
// argument, so it is not a guard concern.
//
// Only the options that precede command's own argument count: a -v after the
// command name belongs to the nested command (`command rm -v`), not to
// command(1).
func hasCommandLookupFlag(words []word) bool {
	for _, w := range words {
		if w.dynamic {
			return false
		}
		text := w.text
		if text == "--" || text == "-" || !strings.HasPrefix(text, "-") {
			return false
		}
		if !strings.HasPrefix(text, "--") && strings.ContainsAny(text[1:], "vV") {
			return true
		}
	}
	return false
}
