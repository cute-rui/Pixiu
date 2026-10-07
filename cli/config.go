package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// File is ~/.config/pixiu.toml.
type File struct {
	Remote             string   `toml:"remote"`
	MihomoBinary       string   `toml:"mihomo_binary"`
	WifiBlacklist      []string `toml:"wifi_blacklist"`
	ExternalController string   `toml:"external_controller"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pixiu.toml"), nil
}

func runtimeDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pixiu"), nil
}

func loadFile() (File, error) {
	path, err := configPath()
	if err != nil {
		return File{}, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultFile(), nil
	}
	if err != nil {
		return File{}, err
	}
	var file File
	if err := toml.Unmarshal(body, &file); err != nil {
		return File{}, err
	}
	if file.MihomoBinary == "" {
		file.MihomoBinary = "mihomo"
	}
	if file.ExternalController == "" {
		file.ExternalController = "127.0.0.1:9090"
	}
	return file, nil
}

func defaultFile() File {
	return File{
		MihomoBinary:       "mihomo",
		ExternalController: "127.0.0.1:9090",
		WifiBlacklist:      []string{},
	}
}

func saveFile(file File) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := toml.Marshal(file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
