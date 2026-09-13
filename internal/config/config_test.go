package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hugantdev/zklens/internal/zk"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string {
		return m[k]
	}
}

// TestMain points HOME (and XDG_CONFIG_HOME) at a throwaway directory so
// Load's default config-file lookup can never pick up a real
// ~/.config/zklens/config.json from the developer's machine.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "zklens-config-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	os.Unsetenv("XDG_CONFIG_HOME")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestLoadDefaults(t *testing.T) {
	cfg, _, _, err := Load(nil, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, DefaultHosts) {
		t.Fatalf("Load() Hosts = %v, want defaults %v", cfg.Hosts, DefaultHosts)
	}
	if cfg.SessionTimeout != 0 {
		t.Fatalf("Load() SessionTimeout = %v, want 0 (zk falls back to its own default)", cfg.SessionTimeout)
	}
	if cfg.Auth != nil {
		t.Fatalf("Load() Auth = %+v, want nil", cfg.Auth)
	}
}

func TestLoadFlagsOverrideEverything(t *testing.T) {
	env := map[string]string{
		EnvHosts:          "envhost:2181",
		EnvSessionTimeout: "5s",
		EnvAuthDigest:     "envuser:envpass",
	}
	args := []string{
		"--hosts=flaghost1:2181,flaghost2:2181",
		"--session-timeout=15s",
		"--auth-digest=flaguser:flagpass",
	}

	cfg, _, _, err := Load(args, envFrom(env))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"flaghost1:2181", "flaghost2:2181"}) {
		t.Fatalf("Load() Hosts = %v, want flag hosts", cfg.Hosts)
	}
	if cfg.SessionTimeout != 15*time.Second {
		t.Fatalf("Load() SessionTimeout = %v, want 15s from flag", cfg.SessionTimeout)
	}
	if cfg.Auth == nil || cfg.Auth.Credential != "flaguser:flagpass" {
		t.Fatalf("Load() Auth = %+v, want digest flaguser:flagpass from flag", cfg.Auth)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{"hosts": ["filehost:2181"], "sessionTimeout": "1s"}`)

	env := map[string]string{
		EnvConfigFile:     path,
		EnvHosts:          "envhost:2181",
		EnvSessionTimeout: "5s",
	}

	cfg, _, _, err := Load(nil, envFrom(env))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"envhost:2181"}) {
		t.Fatalf("Load() Hosts = %v, want env to override file", cfg.Hosts)
	}
	if cfg.SessionTimeout != 5*time.Second {
		t.Fatalf("Load() SessionTimeout = %v, want env (5s) to override file (1s)", cfg.SessionTimeout)
	}
}

func TestLoadFileIsUsedWhenNothingElseSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{
		"hosts": ["filehost1:2181", "filehost2:2181"],
		"sessionTimeout": "8s",
		"auth": {"scheme": "digest", "credential": "fileuser:filepass"}
	}`)

	cfg, _, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"filehost1:2181", "filehost2:2181"}) {
		t.Fatalf("Load() Hosts = %v, want file hosts", cfg.Hosts)
	}
	if cfg.SessionTimeout != 8*time.Second {
		t.Fatalf("Load() SessionTimeout = %v, want 8s from file", cfg.SessionTimeout)
	}
	if cfg.Auth == nil || cfg.Auth.Scheme != zk.AuthSchemeDigest || cfg.Auth.Credential != "fileuser:filepass" {
		t.Fatalf("Load() Auth = %+v, want digest fileuser:filepass from file", cfg.Auth)
	}
}

func TestLoadConfigFlagOverridesConfigEnv(t *testing.T) {
	dir := t.TempDir()
	flagPath := filepath.Join(dir, "flag.json")
	envPath := filepath.Join(dir, "env.json")
	writeFile(t, flagPath, `{"hosts": ["fromflagfile:2181"]}`)
	writeFile(t, envPath, `{"hosts": ["fromenvfile:2181"]}`)

	cfg, _, _, err := Load([]string{"--config=" + flagPath}, envFrom(map[string]string{EnvConfigFile: envPath}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"fromflagfile:2181"}) {
		t.Fatalf("Load() Hosts = %v, want the --config flag's file to win over %s", cfg.Hosts, EnvConfigFile)
	}
}

func TestLoadHostsSplittingTrimsAndDropsEmpty(t *testing.T) {
	cfg, _, _, err := Load([]string{"--hosts= a:1 , b:2 ,,c:3"}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	want := []string{"a:1", "b:2", "c:3"}
	if !reflect.DeepEqual(cfg.Hosts, want) {
		t.Fatalf("Load() Hosts = %v, want %v", cfg.Hosts, want)
	}
}

func TestLoadInvalidSessionTimeoutFlag(t *testing.T) {
	_, _, _, err := Load([]string{"--session-timeout=notaduration"}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with invalid --session-timeout = nil error, want error")
	}
}

func TestLoadInvalidSessionTimeoutEnv(t *testing.T) {
	_, _, _, err := Load(nil, envFrom(map[string]string{EnvSessionTimeout: "notaduration"}))
	if err == nil {
		t.Fatal("Load() with invalid env session timeout = nil error, want error")
	}
}

func TestLoadInvalidSessionTimeoutInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{"sessionTimeout": "notaduration"}`)

	_, _, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with invalid file sessionTimeout = nil error, want error")
	}
}

func TestLoadMalformedConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{not valid json`)

	_, _, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with malformed JSON config = nil error, want error")
	}
}

func TestLoadMissingConfigFile(t *testing.T) {
	_, _, _, err := Load([]string{"--config=/nonexistent/path/zklens.json"}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with a missing config file = nil error, want error")
	}
}

func TestLoadHelpFlag(t *testing.T) {
	_, _, _, err := Load([]string{"--help"}, envFrom(nil))
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("Load() with --help error = %v, want flag.ErrHelp", err)
	}
}

func TestLoadUnknownFlag(t *testing.T) {
	_, _, _, err := Load([]string{"--does-not-exist"}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with an unknown flag = nil error, want error")
	}
	if errors.Is(err, flag.ErrHelp) {
		t.Fatal("Load() with an unknown flag returned flag.ErrHelp, want a distinct error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}
}

// --- contexts ----------------------------------------------------------------

const contextsFile = `{
	"hosts": ["flathost:2181"],
	"contexts": [
		{"name": "dev", "hosts": ["dev1:2181"]},
		{"name": "prod", "hosts": ["prod1:2181", "prod2:2181"], "sessionTimeout": "30s", "auth": {"scheme": "digest", "credential": "produser:prodpass"}}
	]
}`

func writeContextsFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, contextsFile)
	return path
}

func TestLoadParsesContextsInOrder(t *testing.T) {
	path := writeContextsFile(t)

	_, contexts, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("Load() returned %d contexts, want 2", len(contexts))
	}
	if contexts[0].Name != "dev" || contexts[1].Name != "prod" {
		t.Fatalf("context order = %q, %q, want dev, prod", contexts[0].Name, contexts[1].Name)
	}
	if got := contexts[1].Summary(); !strings.Contains(got, "prod1:2181,prod2:2181") ||
		!strings.Contains(got, "timeout 30s") || !strings.Contains(got, "auth digest") {
		t.Fatalf("prod Summary() = %q, want hosts, timeout and auth details", got)
	}
}

func TestLoadContextFlagSelectsContext(t *testing.T) {
	path := writeContextsFile(t)

	cfg, _, contextName, err := Load([]string{"--config=" + path, "--context=prod"}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if contextName != "prod" {
		t.Fatalf("Load() context name = %q, want prod", contextName)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"prod1:2181", "prod2:2181"}) {
		t.Fatalf("Load() Hosts = %v, want prod hosts", cfg.Hosts)
	}
	if cfg.SessionTimeout != 30*time.Second {
		t.Fatalf("Load() SessionTimeout = %v, want 30s from context", cfg.SessionTimeout)
	}
	if cfg.Auth == nil || cfg.Auth.Credential != "produser:prodpass" {
		t.Fatalf("Load() Auth = %+v, want digest produser:prodpass from context", cfg.Auth)
	}
}

func TestLoadContextFlagStillOverriddenByExplicitFlags(t *testing.T) {
	path := writeContextsFile(t)

	cfg, _, contextName, err := Load([]string{"--config=" + path, "--context=prod", "--session-timeout=5s"}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if contextName != "prod" {
		t.Fatalf("Load() context name = %q, want prod", contextName)
	}
	if cfg.SessionTimeout != 5*time.Second {
		t.Fatalf("Load() SessionTimeout = %v, want flag (5s) to override context (30s)", cfg.SessionTimeout)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"prod1:2181", "prod2:2181"}) {
		t.Fatalf("Load() Hosts = %v, want unflagged fields to come from the context", cfg.Hosts)
	}
}

func TestLoadContextEnvSelectsContext(t *testing.T) {
	path := writeContextsFile(t)

	cfg, _, contextName, err := Load([]string{"--config=" + path}, envFrom(map[string]string{EnvContext: "dev"}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if contextName != "dev" {
		t.Fatalf("Load() context name = %q, want dev", contextName)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"dev1:2181"}) {
		t.Fatalf("Load() Hosts = %v, want dev hosts", cfg.Hosts)
	}
}

func TestLoadUnknownContextErrors(t *testing.T) {
	path := writeContextsFile(t)

	_, _, _, err := Load([]string{"--config=" + path, "--context=nope"}, envFrom(nil))
	if err == nil {
		t.Fatal("Load() with --context=nope = nil error, want unknown-context error")
	}
	if !strings.Contains(err.Error(), `unknown context "nope"`) {
		t.Fatalf("Load() error = %v, want it to name the unknown context", err)
	}
}

func TestLoadSingleContextAppliedAutomatically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{"contexts": [{"name": "only", "hosts": ["solo:2181"]}]}`)

	cfg, contexts, contextName, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if contextName != "only" {
		t.Fatalf("Load() context name = %q, want the single context applied automatically", contextName)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"solo:2181"}) {
		t.Fatalf("Load() Hosts = %v, want the single context's hosts", cfg.Hosts)
	}
	if NeedsPicker(contexts, []string{"--config=" + path}, envFrom(nil)) {
		t.Fatal("NeedsPicker() with a single context = true, want false (nothing to choose)")
	}
}

func TestLoadContextWithoutNameOrHostsErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	writeFile(t, path, `{"contexts": [{"hosts": ["a:1"]}]}`)

	_, _, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("Load() error = %v, want a name-is-required error", err)
	}

	writeFile(t, path, `{"contexts": [{"name": "ghost"}]}`)
	_, _, _, err = Load([]string{"--config=" + path}, envFrom(nil))
	if err == nil || !strings.Contains(err.Error(), "hosts is required") {
		t.Fatalf("Load() error = %v, want a hosts-is-required error", err)
	}
}

func TestNeedsPickerRules(t *testing.T) {
	path := writeContextsFile(t)
	args := []string{"--config=" + path}
	contexts, err := loadContexts(t, args)
	if err != nil {
		t.Fatal(err)
	}

	if !NeedsPicker(contexts, args, envFrom(nil)) {
		t.Fatal("NeedsPicker() with two contexts and no selection = false, want true")
	}
	if NeedsPicker(contexts, append(args, "--context=dev"), envFrom(nil)) {
		t.Fatal("NeedsPicker() with --context = true, want false")
	}
	if NeedsPicker(contexts, args, envFrom(map[string]string{EnvContext: "dev"})) {
		t.Fatal("NeedsPicker() with ZKLENS_CONTEXT = true, want false")
	}
	if NeedsPicker(contexts, append(args, "--hosts=explicit:2181"), envFrom(nil)) {
		t.Fatal("NeedsPicker() with explicit --hosts = true, want false")
	}
	if NeedsPicker(contexts, args, envFrom(map[string]string{EnvHosts: "explicit:2181"})) {
		t.Fatal("NeedsPicker() with explicit ZKLENS_HOSTS = true, want false")
	}
	if NeedsPicker(contexts, append(args, "--auth-digest=u:p"), envFrom(nil)) {
		t.Fatal("NeedsPicker() with explicit --auth-digest = true, want false")
	}
	if NeedsPicker(contexts, args, envFrom(map[string]string{EnvAuthDigest: "u:p"})) {
		t.Fatal("NeedsPicker() with explicit ZKLENS_AUTH_DIGEST = true, want false")
	}
	if NeedsPicker(nil, args, envFrom(nil)) {
		t.Fatal("NeedsPicker() with no contexts = true, want false")
	}
}

func loadContexts(t *testing.T, args []string) ([]Context, error) {
	t.Helper()
	_, contexts, _, err := Load(args, envFrom(nil))
	return contexts, err
}

func TestApplyContextLayersOverrides(t *testing.T) {
	path := writeContextsFile(t)
	_, contexts, _, err := Load([]string{"--config=" + path}, envFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	// The user picked "prod" in the startup picker, and session timeout
	// comes from the environment.
	cfg, err := ApplyContext(contexts[1], []string{"--config=" + path},
		envFrom(map[string]string{EnvSessionTimeout: "45s"}))
	if err != nil {
		t.Fatalf("ApplyContext() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"prod1:2181", "prod2:2181"}) {
		t.Fatalf("ApplyContext() Hosts = %v, want prod hosts", cfg.Hosts)
	}
	if cfg.SessionTimeout != 45*time.Second {
		t.Fatalf("ApplyContext() SessionTimeout = %v, want env (45s) to override context (30s)", cfg.SessionTimeout)
	}
	if cfg.Auth == nil || cfg.Auth.Credential != "produser:prodpass" {
		t.Fatalf("ApplyContext() Auth = %+v, want the context's auth preserved", cfg.Auth)
	}
}

// --- default config path -----------------------------------------------------

func TestLoadUsesXDGDefaultConfigPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	writeFile(t, path, `{"hosts": ["xdghost:2181"]}`)

	cfg, _, _, err := Load(nil, envFrom(map[string]string{"XDG_CONFIG_HOME": dir}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"xdghost:2181"}) {
		t.Fatalf("Load() Hosts = %v, want hosts from the XDG default path", cfg.Hosts)
	}
}

func TestLoadUsesHomeDotConfigWhenXDGUnset(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "zklens", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	writeFile(t, path, `{"hosts": ["homehost:2181"]}`)

	cfg, _, _, err := Load(nil, envFrom(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"homehost:2181"}) {
		t.Fatalf("Load() Hosts = %v, want hosts from ~/.config/zklens/config.json", cfg.Hosts)
	}
}

func TestLoadMissingDefaultConfigFileIsNotAnError(t *testing.T) {
	cfg, _, _, err := Load(nil, envFrom(map[string]string{"HOME": t.TempDir()}))
	if err != nil {
		t.Fatalf("Load() with no default config file = %v, want nil (defaults)", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, DefaultHosts) {
		t.Fatalf("Load() Hosts = %v, want defaults %v", cfg.Hosts, DefaultHosts)
	}
}

func TestLoadConfigFlagBeatsDefaultPath(t *testing.T) {
	xdg := t.TempDir()
	defaultPath := filepath.Join(xdg, "zklens", "config.json")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	writeFile(t, defaultPath, `{"hosts": ["defaulthost:2181"]}`)

	flagPath := filepath.Join(t.TempDir(), "explicit.json")
	writeFile(t, flagPath, `{"hosts": ["flaghost:2181"]}`)

	cfg, _, _, err := Load([]string{"--config=" + flagPath}, envFrom(map[string]string{"XDG_CONFIG_HOME": xdg}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"flaghost:2181"}) {
		t.Fatalf("Load() Hosts = %v, want the --config flag to win over the default path", cfg.Hosts)
	}
}

func TestLoadConfigEnvBeatsDefaultPath(t *testing.T) {
	xdg := t.TempDir()
	defaultPath := filepath.Join(xdg, "zklens", "config.json")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	writeFile(t, defaultPath, `{"hosts": ["defaulthost:2181"]}`)

	envPath := filepath.Join(t.TempDir(), "env.json")
	writeFile(t, envPath, `{"hosts": ["envhost:2181"]}`)

	cfg, _, _, err := Load(nil, envFrom(map[string]string{
		"XDG_CONFIG_HOME": xdg,
		EnvConfigFile:     envPath,
	}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"envhost:2181"}) {
		t.Fatalf("Load() Hosts = %v, want %s to win over the default path", cfg.Hosts, EnvConfigFile)
	}
}
