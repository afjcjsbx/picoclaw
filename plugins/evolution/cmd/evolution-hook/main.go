// Command evolution-hook is the optional PicoClaw evolution plugin.
//
// It speaks the PicoClaw process-hook JSON-RPC protocol on stdin/stdout. When
// the plugin is enabled, PicoClaw sends agent.turn.end notifications and this
// process records a slim learning case. In draft/apply mode it can also run the
// cold path and prepare skill drafts. The default build of PicoClaw does not
// include or start this binary.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/plugins/evolution/internal/evolution"
	logger "github.com/sipeed/picoclaw/plugins/evolution/internal/plog"
	"github.com/sipeed/picoclaw/plugins/evolution/internal/server"
)

func main() {
	var (
		dataDir    string
		configPath string
		coldPath   string
		debug      bool
	)
	flag.StringVar(&dataDir, "data-dir", "", "plugin data directory (defaults to PLUGIN_DATA)")
	flag.StringVar(&configPath, "config", "", "optional JSON config file for manual/cold-path runs")
	flag.StringVar(&coldPath, "cold-path", "", "run one cold path for this workspace and exit")
	flag.BoolVar(&debug, "debug", false, "enable debug logging on stderr")
	flag.Parse()

	logger.SetDebug(debug)

	if dataDir == "" {
		dataDir = os.Getenv("PLUGIN_DATA")
	}
	if dataDir == "" {
		fatalf("PLUGIN_DATA or --data-dir is required")
	}

	values, err := loadConfigFile(configPath)
	if err != nil {
		fatalf("load config: %v", err)
	}

	srv := server.New(server.Options{
		DataDir: dataDir,
		Config:  evolution.ParseConfig(values),
	})
	defer srv.Close()

	if coldPath != "" {
		if err := srv.Configure(values); err != nil {
			fatalf("configure: %v", err)
		}
		if err := srv.RunColdPathOnce(context.Background(), coldPath); err != nil {
			fatalf("cold path: %v", err)
		}
		return
	}

	if err := srv.Run(os.Stdin, os.Stdout); err != nil {
		fatalf("hook server: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "evolution-hook: "+format+"\n", args...)
	os.Exit(1)
}

func loadConfigFile(path string) (map[string]string, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(raw))
	for key, value := range raw {
		values[key] = stringify(value)
	}
	return values, nil
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, stringify(item))
		}
		return strings.Join(parts, ",")
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}
