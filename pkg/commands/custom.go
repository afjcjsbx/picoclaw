package commands

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	// CustomCommandsDirName is the directory that holds Markdown prompt
	// commands inside the workspace or the PicoClaw home directory.
	CustomCommandsDirName = "commands"

	customCommandExtension = ".md"
)

var customCommandNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

// CustomCommand is a user-defined prompt command loaded from a Markdown file.
// The Markdown body is the prompt template; only the description is read from
// the optional YAML frontmatter.
type CustomCommand struct {
	Name        string
	Description string
	Template    string
	Source      string
}

// Definition returns the metadata used to list a custom command in help output
// and command palettes. It carries no handler: custom commands expand into a
// normal user prompt instead of running a built-in command handler.
func (c CustomCommand) Definition() Definition {
	return Definition{
		Name:        c.Name,
		Description: c.Description,
		Usage:       "/" + c.Name + " [arguments]",
		ReadOnly:    true,
	}
}

// LoadCustomCommands discovers Markdown prompt commands for a workspace.
// Workspace commands take precedence over commands in the global
// <PICOCLAW_HOME>/commands directory.
func LoadCustomCommands(workspace string) []CustomCommand {
	projectDir := ""
	if strings.TrimSpace(workspace) != "" {
		projectDir = filepath.Join(workspace, CustomCommandsDirName)
	}
	return LoadCustomCommandsFromDirs(
		projectDir,
		filepath.Join(config.GetHome(), CustomCommandsDirName),
	)
}

// LoadCustomCommandsFromDirs discovers commands from explicit project and
// global directories. Project commands override global commands with the same
// name. Directories may be empty or missing.
func LoadCustomCommandsFromDirs(projectDir, globalDir string) []CustomCommand {
	byName := make(map[string]CustomCommand)
	collectCustomCommands(globalDir, byName)
	collectCustomCommands(projectDir, byName)

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]CustomCommand, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

// LookupCustomCommand finds a command by normalized name.
func LookupCustomCommand(commands []CustomCommand, name string) (CustomCommand, bool) {
	if parsed, ok := parseCommandName(name); ok {
		name = parsed
	} else {
		name = normalizeCommandName(name)
	}
	if name == "" {
		return CustomCommand{}, false
	}
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return CustomCommand{}, false
}

// MergeCustomCommandDefinitions appends custom command metadata to a list of
// built-in definitions. Built-in names and aliases always win so a workspace
// file cannot accidentally replace runtime behavior such as /clear.
func MergeCustomCommandDefinitions(
	defs []Definition,
	custom []CustomCommand,
) []Definition {
	out := make([]Definition, len(defs), len(defs)+len(custom))
	copy(out, defs)

	seen := make(map[string]struct{}, len(defs)+len(custom))
	for _, def := range defs {
		seen[normalizeCommandName(def.Name)] = struct{}{}
		for _, alias := range def.Aliases {
			seen[normalizeCommandName(alias)] = struct{}{}
		}
	}

	for _, command := range custom {
		name := normalizeCommandName(command.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, command.Definition())
	}
	return out
}

// CommandArguments returns the raw text after the command token, preserving the
// caller's quoting and internal spacing.
func CommandArguments(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexFunc(trimmed, unicode.IsSpace); idx >= 0 {
		return strings.TrimSpace(trimmed[idx:])
	}
	return ""
}

// SplitCommandArguments splits an argument string on whitespace. Single and
// double quotes group words and are removed from the result.
func SplitCommandArguments(input string) []string {
	var (
		args    []string
		current strings.Builder
		quote   rune
		started bool
	)

	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		args = append(args, current.String())
	}
	return args
}

// ExpandCustomCommandTemplate renders a command template.
//
// $ARGUMENTS is replaced with the complete argument string as entered. $1, $2,
// and higher positions receive parsed arguments; the highest position consumes
// its argument and every argument after it. Missing positions become empty.
// When the template has no placeholder and arguments were supplied, the
// arguments are appended after a blank line.
func ExpandCustomCommandTemplate(template, arguments string) string {
	arguments = strings.TrimSpace(arguments)
	args := SplitCommandArguments(arguments)
	matches := customCommandPlaceholderPattern.FindAllStringSubmatchIndex(template, -1)

	if len(matches) == 0 {
		template = strings.TrimRight(template, "\n")
		if arguments == "" {
			return template
		}
		return template + "\n\n" + arguments
	}

	maxPosition := 0
	for _, match := range matches {
		token := template[match[2]:match[3]]
		if token == "ARGUMENTS" {
			continue
		}
		position, _ := strconv.Atoi(token)
		if position > maxPosition {
			maxPosition = position
		}
	}

	var out strings.Builder
	out.Grow(len(template) + len(arguments))
	last := 0
	for _, match := range matches {
		out.WriteString(template[last:match[0]])
		token := template[match[2]:match[3]]
		if token == "ARGUMENTS" {
			out.WriteString(arguments)
			last = match[1]
			continue
		}

		position, _ := strconv.Atoi(token)
		switch {
		case position <= 0 || position > len(args):
			// Leave the value empty: missing positions expand to nothing.
		case position == maxPosition:
			out.WriteString(strings.Join(args[position-1:], " "))
		default:
			out.WriteString(args[position-1])
		}
		last = match[1]
	}
	out.WriteString(template[last:])
	return out.String()
}

var customCommandPlaceholderPattern = regexp.MustCompile(`\$(ARGUMENTS|[1-9][0-9]*)`)

func collectCustomCommands(root string, into map[string]CustomCommand) {
	root = strings.TrimSpace(root)
	if root == "" {
		return
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // An unreadable entry must not hide the rest of the command set.
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), customCommandExtension) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil //nolint:nilerr // Skip paths that cannot be expressed relative to the root.
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
		name = strings.ToLower(strings.TrimSpace(name))
		if !customCommandNamePattern.MatchString(name) {
			logger.WarnCF("commands", "Skipping custom command file with invalid name", map[string]any{
				"path": path,
				"name": name,
			})
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // Skip unreadable command files.
		}
		description, template := parseCustomCommandDocument(string(data))
		if strings.TrimSpace(template) == "" {
			return nil
		}

		into[name] = CustomCommand{
			Name:        name,
			Description: description,
			Template:    template,
			Source:      path,
		}
		return nil
	})
}

func parseCustomCommandDocument(content string) (description, template string) {
	content = strings.TrimPrefix(content, "\ufeff")
	frontmatter, body, found := splitCustomCommandFrontmatter(content)
	if !found {
		return "", strings.TrimSpace(content)
	}

	var meta struct {
		Description string `yaml:"description"`
	}
	// Invalid frontmatter is ignored so the body still expands as a prompt.
	_ = yaml.Unmarshal([]byte(frontmatter), &meta)
	return strings.TrimSpace(meta.Description), strings.TrimSpace(body)
}

func splitCustomCommandFrontmatter(content string) (frontmatter, body string, found bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\r")) != "---" {
		return "", content, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(strings.TrimSuffix(lines[i], "\r")) != "---" {
			continue
		}
		frontmatter = strings.Join(lines[1:i], "\n")
		body = strings.Join(lines[i+1:], "\n")
		return frontmatter, body, true
	}
	return "", content, false
}
