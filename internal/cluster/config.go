package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/renameio/v2/maybe"
	yaml "go.yaml.in/yaml/v4"
)

const configFilename = "cluster.yaml"

// Mode is the role of a cluster node.
type Mode string

const (
	ModeMaster Mode = "master"
	ModeMember Mode = "member"
)

const (
	ConflictMasterWins = "master_wins"
	ConflictReject     = "reject"
	StatsWindowMin     = "min"
	StatsWindowMaster  = "align_to_master"
)

// Duration wraps time.Duration with human-readable YAML and JSON values.
type Duration struct {
	time.Duration
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("duration %q: %w", text, err)
	}

	d.Duration = parsed

	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) {
	return d.Duration.String(), nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(text)
	if err != nil {
		return err
	}

	d.Duration = parsed

	return nil
}

// Member describes a remote cluster node.
type Member struct {
	Name     string   `yaml:"name" json:"name"`
	APIBase  string   `yaml:"api_base" json:"api_base"`
	Token    string   `yaml:"token" json:"-"`
	Timeout  Duration `yaml:"timeout" json:"timeout"`
	Timezone string   `yaml:"timezone,omitempty" json:"timezone,omitempty"`
}

// SyncConfig controls configuration broadcasting and conflict handling.
type SyncConfig struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Interval Duration `yaml:"interval" json:"interval"`
	OnChange string   `yaml:"on_change" json:"on_change"`
	Conflict string   `yaml:"conflict" json:"conflict"`
}

// StatsConfig controls the aggregate statistics window.
type StatsConfig struct {
	Window string `yaml:"window" json:"window"`
}

// DegradationConfig controls partial-cluster reads.
type DegradationConfig struct {
	SkipOffline bool `yaml:"skip_offline" json:"skip_offline"`
}

// Config is the contents of the cluster section in cluster.yaml.
type Config struct {
	Enabled     bool              `yaml:"enabled" json:"enabled"`
	Mode        Mode              `yaml:"mode" json:"mode"`
	Name        string            `yaml:"name" json:"name"`
	Listen      string            `yaml:"listen" json:"listen"`
	APIBase     string            `yaml:"api_base" json:"api_base"`
	Token       string            `yaml:"token" json:"-"`
	Members     []Member          `yaml:"members" json:"members"`
	Sync        SyncConfig        `yaml:"sync" json:"sync"`
	Stats       StatsConfig       `yaml:"stats" json:"stats"`
	Degradation DegradationConfig `yaml:"degradation" json:"degradation"`
}

type configFile struct {
	Cluster *Config `yaml:"cluster"`
}

// DefaultConfig returns disabled configuration with safe cluster defaults.
func DefaultConfig() *Config {
	return &Config{
		Mode:    ModeMaster,
		Name:    "local",
		Listen:  "127.0.0.1:8091",
		APIBase: "http://127.0.0.1:8091",
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

func applyDefaults(conf *Config) {
	defaults := DefaultConfig()
	if conf.Mode == "" {
		conf.Mode = defaults.Mode
	}
	if conf.Name == "" {
		conf.Name = defaults.Name
	}
	if conf.Listen == "" {
		conf.Listen = defaults.Listen
	}
	if conf.APIBase == "" {
		conf.APIBase = defaults.APIBase
	}
	if conf.Sync.Interval.Duration == 0 {
		conf.Sync.Interval = defaults.Sync.Interval
	}
	if conf.Sync.OnChange == "" {
		conf.Sync.OnChange = defaults.Sync.OnChange
	}
	if conf.Sync.Conflict == "" {
		conf.Sync.Conflict = defaults.Sync.Conflict
	}
	if conf.Stats.Window == "" {
		conf.Stats.Window = defaults.Stats.Window
	}
	for i := range conf.Members {
		if conf.Members[i].Timeout.Duration == 0 {
			conf.Members[i].Timeout = Duration{5 * time.Second}
		}
	}
}

// LoadConfig reads cluster.yaml.  A missing file returns disabled defaults.
func LoadConfig(workDir string) (*Config, error) {
	path := filepath.Join(workDir, configFilename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configFilename, err)
	}

	conf := DefaultConfig()
	file := configFile{Cluster: conf}
	if err = yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configFilename, err)
	}
	if file.Cluster == nil {
		return nil, fmt.Errorf("%s: missing cluster section", configFilename)
	}

	applyDefaults(file.Cluster)
	if err = file.Cluster.Validate(); err != nil {
		return nil, fmt.Errorf("validating %s: %w", configFilename, err)
	}

	return file.Cluster, nil
}

// SaveConfig validates and atomically writes cluster.yaml with private permissions.
func SaveConfig(workDir string, conf *Config) error {
	if conf == nil {
		return errors.New("nil cluster config")
	}
	applyDefaults(conf)
	if err := conf.Validate(); err != nil {
		return err
	}

	data, err := yaml.Marshal(configFile{Cluster: conf})
	if err != nil {
		return fmt.Errorf("encoding cluster config: %w", err)
	}

	path := filepath.Join(workDir, configFilename)
	if err = maybe.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", configFilename, err)
	}

	return nil
}

// Validate checks all cluster configuration fields.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("nil cluster config")
	}
	if !c.Enabled {
		return nil
	}
	if c.Mode != ModeMaster && c.Mode != ModeMember {
		return fmt.Errorf("invalid mode %q", c.Mode)
	}
	if c.Name == "" || c.Token == "" {
		return errors.New("name and token are required")
	}
	if _, err := netip.ParseAddrPort(c.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if err := validateAPIBase(c.APIBase); err != nil {
		return err
	}
	if c.Sync.Interval.Duration <= 0 || c.Sync.OnChange != "broadcast" {
		return errors.New("sync interval must be positive and on_change must be broadcast")
	}
	if c.Sync.Conflict != ConflictMasterWins && c.Sync.Conflict != ConflictReject {
		return fmt.Errorf("invalid conflict policy %q", c.Sync.Conflict)
	}
	if c.Stats.Window != StatsWindowMin && c.Stats.Window != StatsWindowMaster {
		return fmt.Errorf("invalid stats window %q", c.Stats.Window)
	}

	seen := map[string]bool{c.Name: true}
	for _, member := range c.Members {
		if member.Name == "" || member.Token == "" {
			return errors.New("member name and token are required")
		}
		if seen[member.Name] {
			return fmt.Errorf("duplicate node name %q", member.Name)
		}
		seen[member.Name] = true
		if err := validateAPIBase(member.APIBase); err != nil {
			return fmt.Errorf("member %q: %w", member.Name, err)
		}
		if member.Timeout.Duration <= 0 {
			return fmt.Errorf("member %q: timeout must be positive", member.Name)
		}
	}

	return nil
}

func validateAPIBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid API base URL %q", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("API base URL %q contains unsupported components", raw)
	}

	return nil
}

// PublicStatus returns a deep copy with all credentials removed.
func (c *Config) PublicStatus() *Config {
	if c == nil {
		return nil
	}

	clone := *c
	clone.Token = ""
	clone.Members = append([]Member(nil), c.Members...)
	for i := range clone.Members {
		clone.Members[i].Token = ""
	}

	return &clone
}
