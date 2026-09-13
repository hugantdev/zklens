// Package config loads zklens's ZooKeeper connection settings from flags,
// environment variables and an optional JSON config file, and turns them
// into a zk.Config for internal/zk to consume. It knows nothing about the
// TUI.
//
// The config file may declare named contexts (see fileConfig): predefined
// connection profiles the user picks between at startup. Flags and
// environment variables still override individual fields of whichever
// profile ends up selected.
package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hugantdev/zklens/internal/zk"
)

// Environment variable names read by Load.
const (
	EnvHosts          = "ZKLENS_HOSTS"
	EnvSessionTimeout = "ZKLENS_SESSION_TIMEOUT"
	EnvAuthDigest     = "ZKLENS_AUTH_DIGEST"
	EnvConfigFile     = "ZKLENS_CONFIG"
	EnvContext        = "ZKLENS_CONTEXT"
)

// DefaultHosts is used when no source (flag, env, file) specifies any
// hosts.
var DefaultHosts = []string{"localhost:2181"}

// authConfig is the JSON representation of digest (or other) credentials.
type authConfig struct {
	Scheme     string `json:"scheme"`
	Credential string `json:"credential"`
}

// profile is one set of connection settings, either the file's top-level
// (legacy flat format) or a single named context within it, e.g.:
//
//	{"hosts": ["localhost:2181"], "sessionTimeout": "10s",
//	 "auth": {"scheme": "digest", "credential": "user:password"}}
type profile struct {
	Hosts          []string    `json:"hosts,omitempty"`
	SessionTimeout string      `json:"sessionTimeout,omitempty"`
	Auth           *authConfig `json:"auth,omitempty"`
}

// contextConfig is a profile with a name, as listed in the file's
// "contexts" array.
type contextConfig struct {
	Name string `json:"name"`
	profile
}

// fileConfig is the on-disk JSON representation. The flat fields stay
// supported for backward compatibility; "contexts" adds named profiles:
//
//	{
//	  "contexts": [
//	    {"name": "local", "hosts": ["localhost:2181"]},
//	    {"name": "prod", "hosts": ["zk1:2181"], "auth": {"scheme": "digest", "credential": "u:p"}}
//	  ]
//	}
type fileConfig struct {
	profile
	Contexts []contextConfig `json:"contexts,omitempty"`
}

// Context is a named connection profile declared in the config file.
type Context struct {
	Name    string
	profile profile
}

// Summary renders the context's connection details for display next to its
// name (e.g. in the startup picker).
func (c Context) Summary() string {
	parts := []string{strings.Join(c.profile.Hosts, ",")}
	if c.profile.SessionTimeout != "" {
		parts = append(parts, "timeout "+c.profile.SessionTimeout)
	}
	if c.profile.Auth != nil {
		parts = append(parts, "auth "+c.profile.Auth.Scheme)
	}
	return strings.Join(parts, " • ")
}

// cliFlags holds the parsed command-line flags Load cares about.
type cliFlags struct {
	hosts          string
	sessionTimeout string
	authDigest     string
	context        string
	configFile     string
}

func parseFlags(args []string) (cliFlags, error) {
	var f cliFlags
	fs := flag.NewFlagSet("zklens", flag.ContinueOnError)
	fs.StringVar(&f.hosts, "hosts", "", "comma-separated list of ZooKeeper host:port ensemble members")
	fs.StringVar(&f.sessionTimeout, "session-timeout", "", "ZooKeeper session timeout, e.g. 10s")
	fs.StringVar(&f.authDigest, "auth-digest", "", "digest auth credential as user:password")
	fs.StringVar(&f.context, "context", "", "config-file context to connect to")
	fs.StringVar(&f.configFile, "config", "", "path to a JSON config file")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, err
	}
	return f, nil
}

// Load builds a zk.Config by layering settings from, in increasing order
// of precedence: defaults, the config file (flat profile or a selected
// context), environment variables, and command-line flags.
//
// Context selection follows: --context flag, then ZKLENS_CONTEXT, then the
// file's only context (when there is exactly one and no explicit
// connection settings were given anywhere else). The returned context name
// is empty when the flat profile was used. When several contexts are
// declared and none was selected, Load leaves the flat profile in the
// returned config and the caller is expected to ask the user (see
// NeedsPicker and ApplyContext).
//
// args is typically os.Args[1:]. getenv defaults to os.Getenv when nil,
// which lets tests inject a fake environment.
func Load(args []string, getenv func(string) string) (zk.Config, []Context, string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	f, err := parseFlags(args)
	if err != nil {
		return zk.Config{}, nil, "", err
	}

	var fc fileConfig
	path := f.configFile
	explicitFile := path != ""
	if path == "" {
		path = getenv(EnvConfigFile)
		explicitFile = path != ""
	}
	if path == "" {
		path = defaultConfigPath(getenv)
	}
	if path != "" {
		if !explicitFile {
			// The conventional location is optional: its absence just
			// means "no config file", not an error.
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				path = ""
			}
		}
		if path != "" {
			loaded, err := loadFile(path)
			if err != nil {
				return zk.Config{}, nil, "", err
			}
			fc = loaded
		}
	}
	contexts, err := parseContexts(fc.Contexts)
	if err != nil {
		return zk.Config{}, nil, "", err
	}

	var base profile
	contextName := ""
	switch sel := firstNonEmpty(f.context, getenv(EnvContext)); {
	case sel != "":
		ctx, ok := findContext(contexts, sel)
		if !ok {
			return zk.Config{}, nil, "", fmt.Errorf("config: unknown context %q", sel)
		}
		base, contextName = ctx.profile, ctx.Name
	case len(contexts) == 1 && !hasExplicitConn(f, getenv):
		base, contextName = contexts[0].profile, contexts[0].Name
	default:
		base = fc.profile
	}

	var cfg zk.Config
	if err := applyProfile(&cfg, base); err != nil {
		return zk.Config{}, nil, "", err
	}
	if err := applyOverrides(&cfg, f, getenv); err != nil {
		return zk.Config{}, nil, "", err
	}
	if len(cfg.Hosts) == 0 {
		cfg.Hosts = DefaultHosts
	}
	return cfg, contexts, contextName, nil
}

// NeedsPicker reports whether the startup should ask the user to pick a
// context: more than one is declared, and nothing already decided the
// connection (no --context or ZKLENS_CONTEXT, and no explicit hosts or
// auth from flags or environment).
func NeedsPicker(contexts []Context, args []string, getenv func(string) string) bool {
	if len(contexts) < 2 {
		return false
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	f, err := parseFlags(args)
	if err != nil {
		return false
	}
	if f.context != "" || getenv(EnvContext) != "" {
		return false
	}
	return !hasExplicitConn(f, getenv)
}

// ApplyContext builds a zk.Config from a context chosen at runtime (e.g.
// by the startup picker), layering the same environment and flag
// overrides on top that Load would apply.
func ApplyContext(ctx Context, args []string, getenv func(string) string) (zk.Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	f, err := parseFlags(args)
	if err != nil {
		return zk.Config{}, err
	}
	var cfg zk.Config
	if err := applyProfile(&cfg, ctx.profile); err != nil {
		return zk.Config{}, err
	}
	if err := applyOverrides(&cfg, f, getenv); err != nil {
		return zk.Config{}, err
	}
	if len(cfg.Hosts) == 0 {
		cfg.Hosts = DefaultHosts
	}
	return cfg, nil
}

// hasExplicitConn reports whether hosts or auth were given directly via
// flags or environment — an explicit connection the user clearly intends,
// which should bypass context selection.
func hasExplicitConn(f cliFlags, getenv func(string) string) bool {
	return f.hosts != "" || f.authDigest != "" ||
		getenv(EnvHosts) != "" || getenv(EnvAuthDigest) != ""
}

func parseContexts(in []contextConfig) ([]Context, error) {
	out := make([]Context, 0, len(in))
	for i, c := range in {
		if c.Name == "" {
			return nil, fmt.Errorf("config: contexts[%d]: name is required", i)
		}
		if len(c.Hosts) == 0 {
			return nil, fmt.Errorf("config: contexts[%d] %q: hosts is required", i, c.Name)
		}
		out = append(out, Context{Name: c.Name, profile: c.profile})
	}
	return out, nil
}

func findContext(contexts []Context, name string) (Context, bool) {
	for _, c := range contexts {
		if c.Name == name {
			return c, true
		}
	}
	return Context{}, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitHosts(v string) []string {
	parts := strings.Split(v, ",")
	hosts := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			hosts = append(hosts, p)
		}
	}
	return hosts
}

func loadFile(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return fileConfig{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return fc, nil
}

// defaultConfigPath returns the conventional config file location used
// when neither --config nor ZKLENS_CONFIG is set:
// $XDG_CONFIG_HOME/zklens/config.json, or ~/.config/zklens/config.json when
// XDG_CONFIG_HOME is unset. It returns "" when no home directory can be
// determined.
func defaultConfigPath(getenv func(string) string) string {
	if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "zklens", "config.json")
	}
	home := getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return ""
		}
	}
	return filepath.Join(home, ".config", "zklens", "config.json")
}

// applyProfile copies a file-level profile into cfg, parsing the duration
// string.
func applyProfile(cfg *zk.Config, p profile) error {
	if len(p.Hosts) > 0 {
		cfg.Hosts = p.Hosts
	}
	if p.SessionTimeout != "" {
		d, err := time.ParseDuration(p.SessionTimeout)
		if err != nil {
			return fmt.Errorf("config: parse sessionTimeout: %w", err)
		}
		cfg.SessionTimeout = d
	}
	if p.Auth != nil {
		cfg.Auth = &zk.Auth{
			Scheme:     zk.AuthScheme(p.Auth.Scheme),
			Credential: p.Auth.Credential,
		}
	}
	return nil
}

// applyOverrides layers environment variables and then command-line flags
// on top of cfg, field by field.
func applyOverrides(cfg *zk.Config, f cliFlags, getenv func(string) string) error {
	if v := getenv(EnvHosts); v != "" {
		cfg.Hosts = splitHosts(v)
	}
	if v := getenv(EnvSessionTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: parse %s: %w", EnvSessionTimeout, err)
		}
		cfg.SessionTimeout = d
	}
	if v := getenv(EnvAuthDigest); v != "" {
		cfg.Auth = &zk.Auth{Scheme: zk.AuthSchemeDigest, Credential: v}
	}

	if f.hosts != "" {
		cfg.Hosts = splitHosts(f.hosts)
	}
	if f.sessionTimeout != "" {
		d, err := time.ParseDuration(f.sessionTimeout)
		if err != nil {
			return fmt.Errorf("config: parse --session-timeout: %w", err)
		}
		cfg.SessionTimeout = d
	}
	if f.authDigest != "" {
		cfg.Auth = &zk.Auth{Scheme: zk.AuthSchemeDigest, Credential: f.authDigest}
	}
	return nil
}
