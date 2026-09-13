package zk

import (
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid minimal",
			cfg:     Config{Hosts: []string{"localhost:2181"}},
			wantErr: false,
		},
		{
			name:    "valid with session timeout and auth",
			cfg:     Config{Hosts: []string{"a:2181", "b:2181"}, SessionTimeout: 5 * time.Second, Auth: &Auth{Scheme: AuthSchemeDigest, Credential: "user:pass"}},
			wantErr: false,
		},
		{
			name:    "no hosts",
			cfg:     Config{},
			wantErr: true,
		},
		{
			name:    "empty host entry",
			cfg:     Config{Hosts: []string{"localhost:2181", ""}},
			wantErr: true,
		},
		{
			name:    "negative session timeout",
			cfg:     Config{Hosts: []string{"localhost:2181"}, SessionTimeout: -1 * time.Second},
			wantErr: true,
		},
		{
			name:    "auth with empty scheme",
			cfg:     Config{Hosts: []string{"localhost:2181"}, Auth: &Auth{Scheme: "", Credential: "user:pass"}},
			wantErr: true,
		},
		{
			name:    "auth with empty credential",
			cfg:     Config{Hosts: []string{"localhost:2181"}, Auth: &Auth{Scheme: AuthSchemeDigest, Credential: ""}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestConfigSessionTimeoutOrDefault(t *testing.T) {
	if got := (Config{}).sessionTimeoutOrDefault(); got != DefaultSessionTimeout {
		t.Fatalf("sessionTimeoutOrDefault() with zero = %v, want default %v", got, DefaultSessionTimeout)
	}

	custom := 30 * time.Second
	if got := (Config{SessionTimeout: custom}).sessionTimeoutOrDefault(); got != custom {
		t.Fatalf("sessionTimeoutOrDefault() with custom = %v, want %v", got, custom)
	}
}
