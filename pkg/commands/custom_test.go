package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/logger"
)

func writeCustomCommand(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadCustomCommandsFromDirs_DiscoveryAndPrecedence(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()

	writeCustomCommand(t, filepath.Join(globalDir, "review.md"),
		"---\ndescription: Global review\n---\n\nGlobal $ARGUMENTS")
	writeCustomCommand(t, filepath.Join(projectDir, "review.md"),
		"---\ndescription: Project review\n---\n\nProject $ARGUMENTS")
	writeCustomCommand(t, filepath.Join(projectDir, "team", "review.md"),
		"Team review $1.")

	// These must be ignored: wrong extension, hidden files/directories, and
	// files whose stem is not a valid command name.
	writeCustomCommand(t, filepath.Join(projectDir, "notes.txt"), "not a command")
	writeCustomCommand(t, filepath.Join(projectDir, ".hidden.md"), "hidden")
	writeCustomCommand(t, filepath.Join(projectDir, ".hidden", "review.md"), "hidden dir")
	writeCustomCommand(t, filepath.Join(projectDir, "spaced name.md"), "spaced")
	writeCustomCommand(t, filepath.Join(projectDir, "empty.md"), "---\ndescription: empty\n---\n\n")

	commands := LoadCustomCommandsFromDirs(projectDir, globalDir)
	if len(commands) != 2 {
		t.Fatalf("len(commands) = %d, want 2: %+v", len(commands), commands)
	}
	if commands[0].Name != "review" {
		t.Fatalf("commands[0].Name = %q, want review", commands[0].Name)
	}
	if commands[0].Description != "Project review" {
		t.Fatalf("project command did not override global: %+v", commands[0])
	}
	if commands[0].Template != "Project $ARGUMENTS" {
		t.Fatalf("template = %q, want project template", commands[0].Template)
	}
	if commands[0].Source != filepath.Join(projectDir, "review.md") {
		t.Fatalf("source = %q, want project file", commands[0].Source)
	}

	if commands[1].Name != "team/review" {
		t.Fatalf("commands[1].Name = %q, want team/review", commands[1].Name)
	}
	if commands[1].Description != "" {
		t.Fatalf("description = %q, want empty", commands[1].Description)
	}
	if commands[1].Template != "Team review $1." {
		t.Fatalf("template = %q, want nested template", commands[1].Template)
	}
}

func TestLoadCustomCommandsFromDirs_LogsInvalidName(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "commands.log")
	previousLevel := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging() error = %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(previousLevel)
	})

	projectDir := t.TempDir()
	writeCustomCommand(t, filepath.Join(projectDir, "bad name.md"), "bad")

	if commands := LoadCustomCommandsFromDirs(projectDir, ""); len(commands) != 0 {
		t.Fatalf("len(commands) = %d, want 0", len(commands))
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", logFile, err)
	}
	logOutput := string(data)
	if !strings.Contains(logOutput, "Skipping custom command file with invalid name") {
		t.Fatalf("invalid-name warning missing from log:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "bad name.md") {
		t.Fatalf("log does not identify the invalid file:\n%s", logOutput)
	}
}

func TestLoadCustomCommandsFromDirs_MissingDirectories(t *testing.T) {
	commands := LoadCustomCommandsFromDirs("", filepath.Join(t.TempDir(), "missing"))
	if len(commands) != 0 {
		t.Fatalf("len(commands) = %d, want 0", len(commands))
	}
}

func TestLoadCustomCommands_UsesPicoClawHomeForGlobalCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	workspace := t.TempDir()

	writeCustomCommand(t, filepath.Join(home, "commands", "global.md"), "Global")
	writeCustomCommand(t, filepath.Join(workspace, "commands", "local.md"), "Local")

	commands := LoadCustomCommands(workspace)
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	if !reflect.DeepEqual(names, []string{"global", "local"}) {
		t.Fatalf("names = %#v, want [global local]", names)
	}
}

func TestParseCustomCommandDocument_FrontmatterAndBody(t *testing.T) {
	tests := []struct {
		name            string
		content         string
		wantDescription string
		wantTemplate    string
	}{
		{
			name:            "frontmatter",
			content:         "---\ndescription: Review code\n---\n\nReview $ARGUMENTS.",
			wantDescription: "Review code",
			wantTemplate:    "Review $ARGUMENTS.",
		},
		{
			name:         "body only",
			content:      "Review $ARGUMENTS.",
			wantTemplate: "Review $ARGUMENTS.",
		},
		{
			name:            "invalid yaml keeps body",
			content:         "---\ndescription: [broken\n---\n\nBody $ARGUMENTS",
			wantDescription: "",
			wantTemplate:    "Body $ARGUMENTS",
		},
		{
			name:         "unclosed frontmatter is part of the template",
			content:      "---\ndescription: Review\n\nBody $ARGUMENTS",
			wantTemplate: "---\ndescription: Review\n\nBody $ARGUMENTS",
		},
		{
			name:            "bom and crlf",
			content:         "\ufeff---\r\ndescription: Review\r\n---\r\n\r\nBody",
			wantDescription: "Review",
			wantTemplate:    "Body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			description, template := parseCustomCommandDocument(tt.content)
			if description != tt.wantDescription {
				t.Fatalf("description = %q, want %q", description, tt.wantDescription)
			}
			if template != tt.wantTemplate {
				t.Fatalf("template = %q, want %q", template, tt.wantTemplate)
			}
		})
	}
}

func TestSplitCommandArguments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "plain", input: "api stable branch", want: []string{"api", "stable", "branch"}},
		{
			name:  "quoted groups",
			input: `src/auth.ts "error handling"`,
			want:  []string{"src/auth.ts", "error handling"},
		},
		{
			name:  "single quotes",
			input: `check 'a "b" c'`,
			want:  []string{"check", `a "b" c`},
		},
		{name: "empty quoted argument", input: `first "" last`, want: []string{"first", "", "last"}},
		{name: "unclosed quote", input: `first "a b`, want: []string{"first", "a b"}},
		{name: "extra whitespace", input: "  one   two\tthree\n", want: []string{"one", "two", "three"}},
		{name: "empty", input: "   ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCommandArguments(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitCommandArguments(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCommandArguments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "/review src/auth.ts", want: "src/auth.ts"},
		{
			name:  "quoted and inner spacing",
			input: `/review   src/auth.ts "error handling"`,
			want:  `src/auth.ts "error handling"`,
		},
		{name: "nested command", input: "/team/review api stable", want: "api stable"},
		{name: "no arguments", input: "/review", want: ""},
		{name: "whitespace only", input: "  /review  ", want: ""},
		{name: "bang prefix", input: "!review foo", want: "foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CommandArguments(tt.input); got != tt.want {
				t.Fatalf("CommandArguments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandCustomCommandTemplate(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		arguments string
		want      string
	}{
		{
			name:      "arguments exact",
			template:  "Review $ARGUMENTS for bugs.",
			arguments: `src/auth.ts "error handling"`,
			want:      `Review src/auth.ts "error handling" for bugs.`,
		},
		{
			name:      "positional quotes",
			template:  "Check $1. Focus on $2.",
			arguments: `src/auth.ts "error handling"`,
			want:      "Check src/auth.ts. Focus on error handling.",
		},
		{
			name:      "highest position consumes rest",
			template:  "Compare $1 with $2.",
			arguments: "api stable branch",
			want:      "Compare api with stable branch.",
		},
		{
			name:      "missing positions are empty",
			template:  "Compare $1 with $2.",
			arguments: "api",
			want:      "Compare api with .",
		},
		{
			name:      "single position consumes all arguments",
			template:  "Check $1.",
			arguments: "src/auth.ts and more",
			want:      "Check src/auth.ts and more.",
		},
		{
			name:      "multi digit position",
			template:  "$1 $10",
			arguments: "a b c d e f g h i j",
			want:      "a j",
		},
		{
			name:      "fallback appends arguments",
			template:  "Explain this code clearly.\n",
			arguments: "src/cache.ts",
			want:      "Explain this code clearly.\n\nsrc/cache.ts",
		},
		{
			name:     "fallback without arguments",
			template: "Explain this code clearly.",
			want:     "Explain this code clearly.",
		},
		{
			name:     "placeholder without arguments becomes empty",
			template: "Review $ARGUMENTS.",
			want:     "Review .",
		},
		{
			name:      "arguments are not expanded again",
			template:  "$1 and $ARGUMENTS",
			arguments: "$2",
			want:      "$2 and $2",
		},
		{
			name:      "repeated arguments",
			template:  "$ARGUMENTS / $ARGUMENTS",
			arguments: "one two",
			want:      "one two / one two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandCustomCommandTemplate(tt.template, tt.arguments); got != tt.want {
				t.Fatalf(
					"ExpandCustomCommandTemplate(%q, %q) = %q, want %q",
					tt.template,
					tt.arguments,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestMergeCustomCommandDefinitions_BuiltinsWin(t *testing.T) {
	defs := []Definition{
		{Name: "help", Description: "Show help"},
		{Name: "clear", Description: "Clear history", Aliases: []string{"new"}},
	}
	custom := []CustomCommand{
		{Name: "help", Description: "Custom help"},
		{Name: "new", Description: "Custom new"},
		{Name: "review", Description: "Review code"},
	}

	merged := MergeCustomCommandDefinitions(defs, custom)
	if len(merged) != 3 {
		t.Fatalf("len(merged) = %d, want 3: %+v", len(merged), merged)
	}
	if merged[0].Description != "Show help" {
		t.Fatalf("built-in /help was replaced: %+v", merged[0])
	}
	if merged[1].Description != "Clear history" {
		t.Fatalf("built-in /clear was replaced: %+v", merged[1])
	}
	if merged[2].Name != "review" || merged[2].Usage != "/review [arguments]" {
		t.Fatalf("custom metadata = %+v, want /review [arguments]", merged[2])
	}
	if len(defs) != 2 {
		t.Fatalf("MergeCustomCommandDefinitions mutated its input: %+v", defs)
	}
}

func TestLookupCustomCommand(t *testing.T) {
	commands := []CustomCommand{{Name: "team/review"}}

	command, ok := LookupCustomCommand(commands, "/Team/Review")
	if !ok || command.Name != "team/review" {
		t.Fatalf("LookupCustomCommand() = %+v, %v; want team/review, true", command, ok)
	}
	if _, ok := LookupCustomCommand(commands, "missing"); ok {
		t.Fatal("LookupCustomCommand() found a missing command")
	}
	if _, ok := LookupCustomCommand(commands, ""); ok {
		t.Fatal("LookupCustomCommand() found an empty command")
	}
}
