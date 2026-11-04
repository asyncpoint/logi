package config

import (
	"fmt"
	"strings"
)

// parseYAML reads the small YAML subset logi's configuration needs: nested
// "key: value" mappings by indentation, '#' comments, quoted or bare scalars.
// All scalars are returned as strings; nested mappings as map[string]any.
// It deliberately avoids a full YAML dependency (small, dependency-free binary).
func parseYAML(src string) (map[string]any, error) {
	root := map[string]any{}
	type level struct {
		indent int
		m      map[string]any
	}
	stack := []level{{-1, root}}
	for n, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line[:len(line)-len(strings.TrimLeft(line, " \t"))], "\t") {
			return nil, fmt.Errorf("line %d: tabs are not allowed for indentation", n+1)
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := strings.TrimSpace(line)
		i := strings.Index(body, ":")
		if i <= 0 {
			return nil, fmt.Errorf("line %d: expected 'key: value'", n+1)
		}
		key := strings.Trim(strings.TrimSpace(body[:i]), `"'`)
		val := strings.TrimSpace(body[i+1:])
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].m
		if val == "" {
			child := map[string]any{}
			parent[key] = child
			stack = append(stack, level{indent, child})
			continue
		}
		parent[key] = strings.Trim(val, `"'`)
	}
	return root, nil
}

func stripComment(s string) string {
	inQ := byte(0)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inQ != 0:
			if c == inQ {
				inQ = 0
			}
		case c == '"' || c == '\'':
			inQ = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}
