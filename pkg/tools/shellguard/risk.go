// Package shellguard classifies shell commands before PicoClaw executes them.
//
// It parses a command with mvdan.cc/sh/v3/syntax and walks the AST to find every
// simple command, including commands nested in pipelines, conditionals and
// command substitutions. Each command is mapped to a four-level risk score and
// compared against a configurable threshold. This catches indirection that
// regex deny lists miss (for example `x=rm; $x -rf /`) without spawning a
// process, and it does not replace the actual shell: the exec tool still runs
// the command through sh -c, so background sessions, PTY mode and process-tree
// termination keep working.
package shellguard

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// RiskLevel is the potential danger of a shell command.
type RiskLevel int

const (
	// RiskLow covers read-only, informational commands.
	RiskLow RiskLevel = iota
	// RiskMedium covers commands that modify files or perform network reads.
	RiskMedium
	// RiskHigh covers destructive or system-modifying commands.
	RiskHigh
	// RiskCritical covers privilege escalation and always-dangerous commands.
	RiskCritical
)

// String returns the lower-case name of a risk level.
func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseRiskLevel converts a configuration string to a RiskLevel.
func ParseRiskLevel(s string) (RiskLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return RiskLow, nil
	case "medium", "":
		return RiskMedium, nil
	case "high":
		return RiskHigh, nil
	case "critical":
		return RiskCritical, nil
	default:
		return RiskMedium, fmt.Errorf(
			"unknown risk level %q, must be one of: low, medium, high, critical", s,
		)
	}
}

// ArgModifier elevates a command's risk level when all its tokens are present.
// Matching is order-independent and uses argv after normalizeFlags.
type ArgModifier struct {
	Args  []string
	Level RiskLevel
}

// commandRiskTable maps a command name to its base risk level. Commands that are
// not listed default to RiskMedium, so new tools are not silently trusted.
var commandRiskTable = map[string]RiskLevel{
	// Low: read-only, informational.
	"ls": RiskLow, "cat": RiskLow, "head": RiskLow, "tail": RiskLow,
	"grep": RiskLow, "egrep": RiskLow, "fgrep": RiskLow, "find": RiskLow,
	"wc": RiskLow, "echo": RiskLow, "printf": RiskLow, "pwd": RiskLow,
	"whoami": RiskLow, "id": RiskLow, "date": RiskLow, "uname": RiskLow,
	"hostname": RiskLow, "env": RiskLow, "printenv": RiskLow, "which": RiskLow,
	"type": RiskLow, "file": RiskLow, "stat": RiskLow, "readlink": RiskLow,
	"realpath": RiskLow, "basename": RiskLow, "dirname": RiskLow, "sort": RiskLow,
	"uniq": RiskLow, "cut": RiskLow, "tr": RiskLow, "awk": RiskLow,
	"sed": RiskLow, "diff": RiskLow, "md5sum": RiskLow, "sha256sum": RiskLow,
	"sha1sum": RiskLow, "xxd": RiskLow, "od": RiskLow, "hexdump": RiskLow,
	"strings": RiskLow, "tee": RiskLow, "xargs": RiskMedium, "true": RiskLow,
	"false": RiskLow, "test": RiskLow, "[": RiskLow, "seq": RiskLow,
	"yes": RiskLow, "sleep": RiskLow, "du": RiskLow, "df": RiskLow,
	"free": RiskLow, "top": RiskLow, "ps": RiskLow, "uptime": RiskLow,
	"lsof": RiskLow, "tree": RiskLow, "less": RiskLow, "more": RiskLow,
	"jq": RiskLow, "yq": RiskLow, "column": RiskLow, "fold": RiskLow,
	"fmt": RiskLow, "rev": RiskLow, "tac": RiskLow, "nl": RiskLow,
	"comm": RiskLow, "join": RiskLow, "paste": RiskLow, "expand": RiskLow,
	"unexpand": RiskLow, "man": RiskLow, "info": RiskLow, "dig": RiskLow,
	"nslookup": RiskLow, "host": RiskLow, "ping": RiskLow, "ss": RiskLow,
	"netstat": RiskLow, "lsblk": RiskLow, "w": RiskLow, "who": RiskLow,
	"last": RiskLow, "bc": RiskLow, "expr": RiskLow, "time": RiskLow,
	"nproc": RiskLow, "arch": RiskLow, "getent": RiskLow, "command": RiskLow,
	"builtin": RiskLow, "nohup": RiskMedium, "nice": RiskLow, "ionice": RiskLow,
	"stdbuf": RiskLow, "timeout": RiskMedium, "watch": RiskMedium, "trap": RiskLow,

	// Medium: file modification, network reads, build tools.
	"cp": RiskMedium, "mv": RiskMedium, "mkdir": RiskMedium, "touch": RiskMedium,
	"ln": RiskMedium, "tar": RiskMedium, "zip": RiskMedium, "unzip": RiskMedium,
	"gzip": RiskMedium, "gunzip": RiskMedium, "bzip2": RiskMedium, "xz": RiskMedium,
	"curl": RiskMedium, "wget": RiskMedium, "git": RiskMedium, "make": RiskMedium,
	"go": RiskMedium, "python": RiskMedium, "python3": RiskMedium, "node": RiskMedium,
	"npm": RiskMedium, "npx": RiskMedium, "yarn": RiskMedium, "pnpm": RiskMedium,
	"pip": RiskMedium, "pip3": RiskMedium, "cargo": RiskMedium, "rustc": RiskMedium,
	"gcc": RiskMedium, "g++": RiskMedium, "clang": RiskMedium, "javac": RiskMedium,
	"java": RiskMedium, "ruby": RiskMedium, "perl": RiskMedium, "php": RiskMedium,
	"patch": RiskMedium, "nano": RiskMedium, "vi": RiskMedium, "vim": RiskMedium,
	"openssl": RiskMedium, "crontab": RiskMedium, "gpg": RiskMedium,
	"sftp": RiskMedium, "ftp": RiskMedium,

	// High: destructive, system-modifying.
	"rm": RiskHigh, "rmdir": RiskHigh, "chmod": RiskHigh, "chown": RiskHigh,
	"chgrp": RiskHigh, "kill": RiskHigh, "pkill": RiskHigh, "killall": RiskHigh,
	"ssh": RiskHigh, "scp": RiskHigh, "rsync": RiskHigh, "docker": RiskHigh,
	"kubectl": RiskHigh, "systemctl": RiskHigh, "service": RiskHigh, "nc": RiskHigh,
	"netcat": RiskHigh, "ncat": RiskHigh, "socat": RiskHigh, "useradd": RiskHigh,
	"userdel": RiskHigh, "usermod": RiskHigh, "passwd": RiskHigh, "chroot": RiskHigh,
	"truncate": RiskHigh, "shred": RiskHigh, "parallel": RiskHigh,
	"podman": RiskHigh, "nerdctl": RiskHigh, "buildah": RiskHigh, "ctr": RiskHigh,
	"systemd-run": RiskHigh, "at": RiskHigh, "batch": RiskHigh, "tmux": RiskHigh,
	"screen": RiskHigh, "start-stop-daemon": RiskHigh, "nsenter": RiskHigh,
	"unshare": RiskHigh, "bwrap": RiskHigh, "firejail": RiskHigh, "proot": RiskHigh,

	// Critical: privilege escalation, always dangerous.
	"sudo": RiskCritical, "su": RiskCritical, "doas": RiskCritical,
	"pkexec": RiskCritical, "dd": RiskCritical, "mkfs": RiskCritical,
	"mkfs.ext2": RiskCritical, "mkfs.ext3": RiskCritical, "mkfs.ext4": RiskCritical,
	"mkfs.xfs": RiskCritical, "mkfs.btrfs": RiskCritical, "mkfs.fat": RiskCritical,
	"mkfs.vfat": RiskCritical, "mkfs.ntfs": RiskCritical, "fdisk": RiskCritical,
	"parted": RiskCritical, "mount": RiskCritical, "umount": RiskCritical,
	"shutdown": RiskCritical, "reboot": RiskCritical, "poweroff": RiskCritical,
	"halt": RiskCritical, "init": RiskCritical, "insmod": RiskCritical,
	"rmmod": RiskCritical, "modprobe": RiskCritical, "iptables": RiskCritical,
	"ip6tables": RiskCritical, "nft": RiskCritical, "chattr": RiskCritical,
	"visudo": RiskCritical, "eval": RiskCritical, "exec": RiskCritical,
	"source": RiskCritical, "runc": RiskCritical, "crun": RiskCritical, ".": RiskCritical,

	// Critical: shells execute arbitrary nested commands. The guard parses a
	// literal -c script recursively; an opaque shell (stdin or a script file)
	// stays critical because its commands cannot be inspected.
	"sh": RiskCritical, "bash": RiskCritical, "zsh": RiskCritical,
	"dash": RiskCritical, "fish": RiskCritical, "csh": RiskCritical,
	"tcsh": RiskCritical, "ksh": RiskCritical, "pwsh": RiskCritical,
	"powershell": RiskCritical, "cmd": RiskCritical,
}

// argumentModifiers maps command names to argument-aware risk adjustments.
// Matching is order-independent and highest-match-wins.
var argumentModifiers = map[string][]ArgModifier{
	"git": {
		{Args: []string{"push", "--force"}, Level: RiskCritical},
		{Args: []string{"push", "-f"}, Level: RiskCritical},
		{Args: []string{"push"}, Level: RiskHigh},
		{Args: []string{"reset", "--hard"}, Level: RiskHigh},
		{Args: []string{"clean", "-f", "-d"}, Level: RiskHigh},
		{Args: []string{"clean", "-f"}, Level: RiskHigh},
	},
	"curl": {
		{Args: []string{"-X", "POST"}, Level: RiskHigh},
		{Args: []string{"-X", "PUT"}, Level: RiskHigh},
		{Args: []string{"-X", "DELETE"}, Level: RiskHigh},
		{Args: []string{"--request", "POST"}, Level: RiskHigh},
		{Args: []string{"--request", "PUT"}, Level: RiskHigh},
		{Args: []string{"--request", "DELETE"}, Level: RiskHigh},
		{Args: []string{"--data"}, Level: RiskHigh},
		{Args: []string{"-d"}, Level: RiskHigh},
		{Args: []string{"--upload-file"}, Level: RiskHigh},
		{Args: []string{"-T"}, Level: RiskHigh},
	},
	"wget": {
		{Args: []string{"--post-data"}, Level: RiskHigh},
		{Args: []string{"--post-file"}, Level: RiskHigh},
	},
	"npm": {
		{Args: []string{"install", "-g"}, Level: RiskHigh},
		{Args: []string{"install", "--global"}, Level: RiskHigh},
		{Args: []string{"publish"}, Level: RiskHigh},
	},
	"pip":  {{Args: []string{"install", "--user"}, Level: RiskHigh}},
	"pip3": {{Args: []string{"install", "--user"}, Level: RiskHigh}},
	"docker": {
		{Args: []string{"run", "--privileged"}, Level: RiskCritical},
		{Args: []string{"run"}, Level: RiskHigh},
		{Args: []string{"exec"}, Level: RiskHigh},
		{Args: []string{"rm"}, Level: RiskHigh},
		{Args: []string{"rmi"}, Level: RiskHigh},
	},
	"apt": {
		{Args: []string{"install"}, Level: RiskHigh},
		{Args: []string{"remove"}, Level: RiskHigh},
		{Args: []string{"purge"}, Level: RiskCritical},
	},
	"apt-get": {
		{Args: []string{"install"}, Level: RiskHigh},
		{Args: []string{"remove"}, Level: RiskHigh},
		{Args: []string{"purge"}, Level: RiskCritical},
	},
	"yum": {
		{Args: []string{"install"}, Level: RiskHigh},
		{Args: []string{"remove"}, Level: RiskHigh},
	},
	"dnf": {
		{Args: []string{"install"}, Level: RiskHigh},
		{Args: []string{"remove"}, Level: RiskHigh},
	},
	"rm": {
		{Args: []string{"-r", "-f"}, Level: RiskCritical},
		{Args: []string{"-R", "-f"}, Level: RiskCritical},
		{Args: []string{"--recursive", "--force"}, Level: RiskCritical},
		{Args: []string{"-r"}, Level: RiskHigh},
		{Args: []string{"-R"}, Level: RiskHigh},
		{Args: []string{"--recursive"}, Level: RiskHigh},
	},
	"kill": {
		{Args: []string{"-9"}, Level: RiskCritical},
		{Args: []string{"-KILL"}, Level: RiskCritical},
		{Args: []string{"-SIGKILL"}, Level: RiskCritical},
	},
	"find": {
		{Args: []string{"-delete"}, Level: RiskHigh},
		{Args: []string{"-exec"}, Level: RiskHigh},
		{Args: []string{"-execdir"}, Level: RiskHigh},
		{Args: []string{"-ok"}, Level: RiskHigh},
	},
	"sed":     {{Args: []string{"-i"}, Level: RiskMedium}},
	"rsync":   {{Args: []string{"--delete"}, Level: RiskCritical}},
	"crontab": {{Args: []string{"-r"}, Level: RiskHigh}},
	"ssh": {
		{Args: []string{"-R"}, Level: RiskHigh},
		{Args: []string{"-L"}, Level: RiskHigh},
	},
	"tar": {{Args: []string{"--to-command"}, Level: RiskCritical}},
}

// ClassifyCommand returns the risk level of a resolved command. args[0] is the
// command name and args[1:] are its arguments.
//
// Precedence is highest-wins: argument modifiers (built-in and user-supplied)
// can only elevate the base level, while overrides replace the built-in base.
func ClassifyCommand(
	args []string,
	overrides map[string]string,
	extraModifiers ...map[string][]ArgModifier,
) RiskLevel {
	if len(args) == 0 {
		return RiskMedium
	}

	cmdName := baseCommand(args[0])
	level, known := commandRiskTable[cmdName]
	if !known {
		level = RiskMedium
	}
	if levelStr, ok := overrides[cmdName]; ok {
		if parsed, err := ParseRiskLevel(levelStr); err == nil {
			level = parsed
		}
	}

	normalizedArgs := normalizeFlags(args[1:])
	level = applyModifiers(normalizedArgs, cmdName, level, argumentModifiers)
	for _, extra := range extraModifiers {
		level = applyModifiers(normalizedArgs, cmdName, level, extra)
	}
	return level
}

func applyModifiers(
	normalizedArgs []string,
	cmdName string,
	baseLevel RiskLevel,
	mods map[string][]ArgModifier,
) RiskLevel {
	entries, ok := mods[cmdName]
	if !ok {
		return baseLevel
	}
	result := baseLevel
	for _, mod := range entries {
		if mod.Level > result && matchArgs(normalizedArgs, mod.Args) {
			result = mod.Level
		}
	}
	return result
}

// IsAllowed reports whether level is at or below threshold.
func IsAllowed(level, threshold RiskLevel) bool {
	return level <= threshold
}

// BlockedError describes a command rejected by the risk classifier.
type BlockedError struct {
	Command   string
	Level     RiskLevel
	Threshold RiskLevel
	Reason    string
}

// Error implements the error interface.
func (e *BlockedError) Error() string {
	return fmt.Sprintf(
		"Command blocked by risk classifier: command=%q risk_level=%s threshold=%s reason=%s",
		e.Command, e.Level, e.Threshold, e.Reason,
	)
}

// NewBlockedError builds a BlockedError from argv.
func NewBlockedError(args []string, level, threshold RiskLevel, reason string) *BlockedError {
	return &BlockedError{
		Command:   FormatCommand(args),
		Level:     level,
		Threshold: threshold,
		Reason:    reason,
	}
}

// FormatCommand renders at most the first five argv tokens for an error.
func FormatCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	const maxArgs = 5
	cmd := args[0]
	end := len(args)
	if end > maxArgs {
		end = maxArgs
	}
	for _, a := range args[1:end] {
		cmd += " " + a
	}
	if len(args) > maxArgs {
		cmd += " ..."
	}
	return cmd
}

// NormalizeCommandKeys rewrites command names to the same basename form used by
// ClassifyCommand, so user config matches paths such as /bin/rm.
func NormalizeCommandKeys[V any](m map[string]V) map[string]V {
	if len(m) == 0 {
		return m
	}
	normalized := make(map[string]V, len(m))
	for k, v := range m {
		normalized[baseCommand(k)] = v
	}
	return normalized
}

// baseCommand extracts the basename from a command path. On Windows it also
// lowercases and strips executable extensions; on Unix those transformations
// are unsafe because they could disguise an arbitrary binary as a known tool.
func baseCommand(cmd string) string {
	if cmd == "" {
		return ""
	}
	name := filepath.Base(cmd)
	if runtime.GOOS != "windows" {
		return name
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(lower, ext) {
			return lower[:len(lower)-len(ext)]
		}
	}
	return lower
}

// normalizeFlags expands grouped short flags (-rf to -r -f) so modifiers match
// equivalent spellings. Long --flag=value forms are left intact; matchArgs
// matches them against a flag+value modifier pair without leaking the value as
// a standalone positional token.
func normalizeFlags(args []string) []string {
	result := make([]string, 0, len(args)*2)
	for _, a := range args {
		if len(a) > 2 && len(a) <= 4 && a[0] == '-' && a[1] != '-' {
			for _, ch := range a[1:] {
				result = append(result, "-"+string(ch))
			}
			continue
		}
		result = append(result, a)
	}
	return result
}

// matchArgs reports whether all pattern tokens are present in args. Matching is
// order-independent. A flag followed by a value also matches the attached
// --flag=value and short -fvalue forms, so curl --request=POST and curl -XPOST
// elevate like their spaced forms. Positional tokens only match standalone argv
// entries, so git log --grep=push does not look like git push.
func matchArgs(args, pattern []string) bool {
	if len(pattern) == 0 {
		return true
	}

	remaining := make([]string, len(args))
	copy(remaining, args)
	matched := make([]bool, len(pattern))

	for i := 0; i+1 < len(pattern); i++ {
		if matched[i] || matched[i+1] {
			continue
		}
		flag, value := pattern[i], pattern[i+1]
		if !strings.HasPrefix(flag, "-") || strings.HasPrefix(value, "-") {
			continue
		}
		for j, arg := range remaining {
			if arg == "" {
				continue
			}
			if arg == flag+"="+value || (!strings.HasPrefix(flag, "--") && arg == flag+value) {
				remaining[j] = ""
				matched[i], matched[i+1] = true, true
				break
			}
		}
	}

	for i, token := range pattern {
		if matched[i] {
			continue
		}
		found := false
		for j, arg := range remaining {
			if arg == "" {
				continue
			}
			if arg == token || flagMatchesArg(token, arg) {
				remaining[j] = ""
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// flagMatchesArg matches --flag=value and short attached forms such as -XPOST
// and -d@file. A single-dash token longer than its flag is treated as an
// attached value, which is conservative for risk elevation.
func flagMatchesArg(token, arg string) bool {
	if !strings.HasPrefix(token, "-") {
		return false
	}
	if strings.HasPrefix(arg, token+"=") {
		return true
	}
	if strings.HasPrefix(token, "--") || len(arg) <= len(token) || !strings.HasPrefix(arg, token) {
		return false
	}
	return arg[len(token)] != '-'
}
