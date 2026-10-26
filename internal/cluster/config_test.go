package cluster

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func validTestConfig() *Config {
	return &Config{
		Enabled: true,
		Mode:    ModeMaster,
		Name:    "master",
		Listen:  "127.0.0.1:8091",
		APIBase: "http://127.0.0.1:8091",
		Token:   "master-secret",
		Members: []Member{{
			Name:     "member",
			APIBase:  "http://127.0.0.1:8092",
			Token:    "member-secret",
			Timeout:  Duration{5 * time.Second},
			Timezone: "Asia/Shanghai",
		}},
		Sync: SyncConfig{
			Enabled:  true,
			Interval: Duration{30 * time.Second},
			OnChange: "broadcast",
			Conflict: ConflictMasterWins,
		},
		Stats:       StatsConfig{Window: StatsWindowMin},
		Degradation: DegradationConfig{SkipOffline: true},
	}
}

func TestLoadConfigMissing(t *testing.T) {
	conf, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if conf.Enabled {
		t.Fatal("missing cluster.yaml must disable clustering")
	}
	if conf.Sync.Interval.Duration != 30*time.Second {
		t.Fatalf("default interval = %v", conf.Sync.Interval.Duration)
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	want := validTestConfig()
	if err := SaveConfig(dir, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Name != want.Name || got.Mode != ModeMaster || len(got.Members) != 1 {
		t.Fatalf("unexpected config: %+v", got)
	}
	if got.Members[0].Timeout.Duration != 5*time.Second {
		t.Fatalf("timeout = %v", got.Members[0].Timeout.Duration)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{{
		name: "missing token",
		edit: func(c *Config) { c.Token = "" },
	}, {
		name: "bad mode",
		edit: func(c *Config) { c.Mode = "standalone" },
	}, {
		name: "bad member URL",
		edit: func(c *Config) { c.Members[0].APIBase = "file:///tmp/socket" },
	}, {
		name: "duplicate member",
		edit: func(c *Config) { c.Members = append(c.Members, c.Members[0]) },
	}, {
		name: "bad window",
		edit: func(c *Config) { c.Stats.Window = "maximum" },
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conf := validTestConfig()
			tc.edit(conf)
			if err := conf.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSaveConfigDoesNotExposeTokensInStatus(t *testing.T) {
	conf := validTestConfig()
	status := conf.PublicStatus()
	if status.Token != "" || status.Members[0].Token != "" {
		t.Fatal("public status contains a token")
	}
}

func TestClusterDisabledMiddleware(t *testing.T) {
	conf := DefaultConfig()
	c, err := New(conf, Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})
	c.Middleware(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/control/stats", nil))
	if !called {
		t.Fatal("disabled middleware did not invoke the original handler")
	}
}

func TestConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}

	dir := t.TempDir()
	if err := SaveConfig(dir, validTestConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, configFilename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
}
