// config.go — MCP server configuration loading.
//
// Reads standard MCP config files (same format as Claude Desktop, Cursor, etc.)
// from two locations:
//
//  1. Global:  $ZUT_HOME/mcp.json
//  2. Project: .zut/mcp.json       (in the current working directory)
//
// Project config overrides global config per-server (shallow merge).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// ServerConfig describes one MCP server entry.
type ServerConfig struct {
	// Stdio transport fields
	Command string            `json:"command,omitempty"` // executable to spawn
	Args    []string          `json:"args,omitempty"`    // arguments
	Env     map[string]string `json:"env,omitempty"`     // extra env vars

	// HTTP transport fields
	Transport string            `json:"transport,omitempty"` // "stdio" (default) | "streamable-http" | "sse"
	URL       string            `json:"url,omitempty"`       // server URL for HTTP transports
	Headers   map[string]string `json:"headers,omitempty"`   // custom HTTP headers

	// Timeouts (in seconds)
	ConnectTimeout int `json:"connectTimeout,omitempty"` // connection timeout (default: 30)
	RequestTimeout int `json:"requestTimeout,omitempty"` // per-request timeout (default: 60)
	IdleTimeout    int `json:"idleTimeout,omitempty"`    // idle timeout before stopping (default: 300)
}

// Config is the top-level MCP configuration.
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// zutHome returns the zut state directory.
func zutHome() string {
	if h := os.Getenv("ZUT_HOME"); h != "" {
		return h
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "zut")
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "zut")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "zut")
	default: // linux, freebsd, etc.
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state", "zut")
	}
}

// loadConfig reads and merges global + project MCP configs.
// cwd is the current working directory (for project config lookup).
func loadConfig(cwd string) (Config, error) {
	cfg := Config{MCPServers: make(map[string]ServerConfig)}

	// 1. Global config
	globalPath := filepath.Join(zutHome(), "mcp.json")
	if err := mergeConfig(&cfg, globalPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, fmt.Errorf("global config %s: %w", globalPath, err)
	}

	// 2. Project config (overrides global per-server)
	if cwd != "" {
		projectPath := filepath.Join(cwd, ".zut", "mcp.json")
		if err := mergeConfig(&cfg, projectPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return cfg, fmt.Errorf("project config %s: %w", projectPath, err)
		}
	}

	return cfg, nil
}

// mergeConfig reads a JSON config file and merges its servers into cfg.
func mergeConfig(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var file Config
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for name, srv := range file.MCPServers {
		// Apply defaults
		if srv.Transport == "" {
			srv.Transport = "stdio"
		}
		if srv.ConnectTimeout == 0 {
			srv.ConnectTimeout = 30
		}
		if srv.RequestTimeout == 0 {
			srv.RequestTimeout = 60
		}
		if srv.IdleTimeout == 0 {
			srv.IdleTimeout = 300
		}
		cfg.MCPServers[name] = srv
	}
	return nil
}
