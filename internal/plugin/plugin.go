package plugin

import (
	"fmt"
	"os"
	"strings"
	"time"

	"larkwrt/internal/config"
	"larkwrt/internal/executor"
)

// StatResult holds the output of one named metric command.
type StatResult struct {
	Label string
	Value string
}

// Installed returns plugins from the list that are detected as installed on the system.
// Detection checks the `detect` path; falls back to `config_file`; assumes installed if neither is set.
func Installed(plugins []config.PluginConfig) []config.PluginConfig {
	var result []config.PluginConfig
	for i := range plugins {
		if isDetected(&plugins[i]) {
			result = append(result, plugins[i])
		}
	}
	return result
}

// Find returns the named plugin only if it is currently installed.
func Find(plugins []config.PluginConfig, name string) (config.PluginConfig, bool) {
	for i := range plugins {
		if strings.EqualFold(plugins[i].Name, name) && isDetected(&plugins[i]) {
			return plugins[i], true
		}
	}
	return config.PluginConfig{}, false
}

func isDetected(p *config.PluginConfig) bool {
	if p.Detect != "" {
		_, err := os.Stat(p.Detect)
		return err == nil
	}
	if p.ConfigFile != "" {
		_, err := os.Stat(p.ConfigFile)
		return err == nil
	}
	return true
}

// RunStatus executes the plugin's status_cmd via sh and returns stdout.
func RunStatus(p config.PluginConfig) (string, error) {
	if p.StatusCmd == "" {
		return "", fmt.Errorf("未配置 status_cmd")
	}
	out, errOut, err := executor.RunUnchecked(10*time.Second, "sh", "-c", p.StatusCmd)
	if err != nil {
		return "", fmt.Errorf("%s", errOut)
	}
	return out, nil
}

// RunStats executes each stat command in parallel (sequential for simplicity) and returns results.
func RunStats(p config.PluginConfig) []StatResult {
	var results []StatResult
	for _, s := range p.Stats {
		out, _, _ := executor.RunUnchecked(5*time.Second, "sh", "-c", s.Cmd)
		results = append(results, StatResult{Label: s.Label, Value: strings.TrimSpace(out)})
	}
	return results
}

// ReadConfig reads the plugin's config file and returns its content.
func ReadConfig(p config.PluginConfig) (string, error) {
	if p.ConfigFile == "" {
		return "", fmt.Errorf("未配置 config_file")
	}
	data, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
