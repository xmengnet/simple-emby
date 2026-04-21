package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	BindAddr      string `json:"bind_addr"`
	MpvPath       string `json:"mpv_path"`
	MpvArgs       string `json:"mpv_args"` // User can add custom args like --fs
	EnableDanmaku bool   `json:"enable_danmaku"`
}

var DefaultConfig = Config{
	BindAddr:      "127.0.0.1:19999",
	MpvPath:       "mpv",
	MpvArgs:       "--fs", // Default to fullscreen
	EnableDanmaku: true,
}

func GetConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "emby-mpv-tray", "config.json"), nil
}

func GetLogPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "emby-mpv-tray", "app.log"), nil
}

func GetDanmakuPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "emby-mpv-tray", "danmaku")
	_ = os.MkdirAll(dir, 0755)
	return dir, nil
}

func LoadConfig() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// If config doesn't exist, try saving the default one
			_ = SaveConfig(&DefaultConfig)
			cfg := DefaultConfig
			return &cfg, nil
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Apply defaults for empty required fields
	if cfg.BindAddr == "" {
		cfg.BindAddr = DefaultConfig.BindAddr
	}
	if cfg.MpvPath == "" {
		cfg.MpvPath = DefaultConfig.MpvPath
	}

	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}
