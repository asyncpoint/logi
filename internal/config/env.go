// Settings taken from LOGI_* environment variables.

package config

import (
	"fmt"
	"strconv"
)

func applyEnv(c *Config, getenv func(string) string) error {
	if v := getenv("LOGI_BUFFER"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("LOGI_BUFFER: %v", err)
		}
		c.Buffer = n
	}
	if v := getenv("LOGI_PARSER"); v != "" {
		c.Parser = v
	}
	if v := getenv("LOGI_COLOR"); v != "" {
		c.Color = v
	}
	if v := getenv("LOGI_FOLLOW"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("LOGI_FOLLOW: %v", err)
		}
		c.Follow = b
	}
	for _, name := range splitList(getenv("LOGI_DISABLE_PLUGINS")) {
		c.Plugins[name] = false
	}
	return nil
}
