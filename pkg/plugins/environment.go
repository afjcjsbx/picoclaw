package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Expand(value, root, data string) string {
	// Replacer never rescans replacement text.
	return strings.NewReplacer("${PLUGIN_ROOT}", root, "${PLUGIN_DATA}", data).Replace(value)
}

func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func reservedEnv(name string) bool {
	key := envKey(name)
	return key == "PLUGIN_ROOT" || key == "PLUGIN_DATA"
}

func Environment(root, data string, configured map[string]string) []string {
	values := map[string]string{}
	// Do not expose provider credentials or arbitrary ambient variables.
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			values[envKey(key)] = value
		}
	}
	for _, key := range sortedKeys(configured) {
		values[envKey(key)] = Expand(configured[key], root, data)
	}
	values["PLUGIN_ROOT"] = root
	values["PLUGIN_DATA"] = data
	result := make([]string, 0, len(values))
	for _, key := range sortedKeys(values) {
		result = append(result, key+"="+values[key])
	}
	return result
}

func prepareDataDirectory(base, id string) (string, error) {
	if mkdirErr := os.MkdirAll(base, 0o700); mkdirErr != nil {
		return "", mkdirErr
	}
	base, err := canonicalRoot(base)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if mkdirErr := root.MkdirAll(id, 0o700); mkdirErr != nil {
		return "", mkdirErr
	}
	info, err := root.Lstat(id)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("plugin data directory must not alias another installation")
	}
	path, err := ResolvePath(base, id)
	if err != nil {
		return "", err
	}
	probe, err := os.CreateTemp(path, ".write-check-")
	if err != nil {
		return "", err
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return "", closeErr
	}
	if removeErr != nil {
		return "", removeErr
	}
	return path, nil
}

func workingDirectory(root, data, value string) (string, error) {
	if value == "" {
		return root, nil
	}
	boundary := root
	switch {
	case strings.HasPrefix(value, "./"):
	case value == "${PLUGIN_ROOT}" || strings.HasPrefix(value, "${PLUGIN_ROOT}/"):
	case value == "${PLUGIN_DATA}" || strings.HasPrefix(value, "${PLUGIN_DATA}/"):
		boundary = data
	default:
		return "", fmt.Errorf("invalid cwd %q", value)
	}
	expanded := Expand(value, root, data)
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(root, expanded)
	}
	resolved, err := ResolvePath(boundary, expanded)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	return resolved, nil
}
