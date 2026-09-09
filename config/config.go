package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	FlareSolverr FlareSolverrConfig `yaml:"flaresolverr"`
	Cache        CacheConfig        `yaml:"cache"`
	Trackers     TrackersConfig     `yaml:"trackers"`
}

type FlareSolverrConfig struct {
	Enabled        bool   `yaml:"enabled"`
	URL            string `yaml:"url"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type CacheConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Path     string `yaml:"path"`
	TTLHours int    `yaml:"ttl_hours"`
}

type ServerConfig struct {
	Port           int `yaml:"port"`
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

type TrackersConfig struct {
	Rutor     RutorConfig     `yaml:"rutor"`
	NNMClub   NNMClubConfig   `yaml:"nnmclub"`
	RuTracker RuTrackerConfig `yaml:"rutracker"`
}

type RutorConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
}

type NNMClubConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BaseURL  string `yaml:"base_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Cookie   string `yaml:"cookie"`
}

type RuTrackerConfig struct {
	Enabled   bool   `yaml:"enabled"`
	BaseURL   string `yaml:"base_url"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	Cookie    string `yaml:"cookie"`
	UserAgent string `yaml:"user_agent"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:           9118,
			TimeoutSeconds: 15,
		},
		FlareSolverr: FlareSolverrConfig{
			Enabled:        false,
			URL:            "http://flaresolverr:8191/v1",
			TimeoutSeconds: 60,
		},
		Cache: CacheConfig{
			Enabled:  true,
			Path:     "cache.db",
			TTLHours: 24,
		},
		Trackers: TrackersConfig{
			Rutor: RutorConfig{
				Enabled: true,
				BaseURL: "https://rutor.info",
			},
			NNMClub: NNMClubConfig{
				Enabled: true,
				BaseURL: "https://nnmclub.to",
			},
			RuTracker: RuTrackerConfig{
				Enabled: true,
				BaseURL: "https://rutracker.org",
			},
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, err
			}
		}
	}

	// Environment variable overrides
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			cfg.Server.Port = p
		}
	}
	if fsURL := os.Getenv("FLARESOLVERR_URL"); fsURL != "" {
		cfg.FlareSolverr.URL = fsURL
		cfg.FlareSolverr.Enabled = true
	}
	if fsEnabled := os.Getenv("FLARESOLVERR_ENABLED"); fsEnabled != "" {
		cfg.FlareSolverr.Enabled = fsEnabled == "true" || fsEnabled == "1"
	}
	if cEnabled := os.Getenv("CACHE_ENABLED"); cEnabled != "" {
		cfg.Cache.Enabled = cEnabled == "true" || cEnabled == "1"
	}
	if cPath := os.Getenv("CACHE_PATH"); cPath != "" {
		cfg.Cache.Path = cPath
	}
	if cTTL := os.Getenv("CACHE_TTL_HOURS"); cTTL != "" {
		if ttl, err := strconv.Atoi(cTTL); err == nil {
			cfg.Cache.TTLHours = ttl
		}
	}
	if u := os.Getenv("NNMCLUB_USERNAME"); u != "" {
		cfg.Trackers.NNMClub.Username = u
	}
	if p := os.Getenv("NNMCLUB_PASSWORD"); p != "" {
		cfg.Trackers.NNMClub.Password = p
	}
	if c := os.Getenv("NNMCLUB_COOKIE"); c != "" {
		cfg.Trackers.NNMClub.Cookie = c
	}
	if u := os.Getenv("RUTRACKER_USERNAME"); u != "" {
		cfg.Trackers.RuTracker.Username = u
	}
	if p := os.Getenv("RUTRACKER_PASSWORD"); p != "" {
		cfg.Trackers.RuTracker.Password = p
	}
	if c := os.Getenv("RUTRACKER_COOKIE"); c != "" {
		cfg.Trackers.RuTracker.Cookie = c
	}
	if ua := os.Getenv("RUTRACKER_USER_AGENT"); ua != "" {
		cfg.Trackers.RuTracker.UserAgent = ua
	}

	return cfg, nil
}
