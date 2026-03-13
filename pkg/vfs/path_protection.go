/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package vfs

import (
	"encoding/json"
	"regexp"
	"sync"
	"syscall"
)

// ProtectionMode defines the type of protection for a path
type ProtectionMode string

const (
	// ProtectionModeReadonly allows read operations but denies write operations
	 ProtectionModeReadonly ProtectionMode = "readonly"
	 // ProtectionModeDeny denies all operations (read and write)
    ProtectionModeDeny ProtectionMode = "deny"
)

// PathProtectionRule defines a single protection rule
type PathProtectionRule struct {
    Pattern string         `json:"pattern"` // Regex pattern to match paths
    Mode    ProtectionMode `json:"mode"`    // Protection mode: "readonly" or "deny"
}

// PathProtectionConfig holds all path protection configuration
type PathProtectionConfig struct {
    Enabled bool                  `json:"enabled"`
    Rules   []PathProtectionRule  `json:"rules"`
}

// PathProtector handles path protection logic
type PathProtector struct {
    config     *PathProtectionConfig
    rules     []*compiledRule
    mountpoint string
    mu         sync.RWMutex
}

type compiledRule struct {
    pattern *regexp.Regexp
    mode    ProtectionMode
}

// NewPathProtector creates a new PathProtector instance
func NewPathProtector(config *PathProtectionConfig, mountpoint string) (*PathProtector, error) {
    if config == nil {
        config = &PathProtectionConfig{Enabled: false}
    }

    pp := &PathProtector{
        config:     config,
        mountpoint: mountpoint,
        rules:      make([]*compiledRule, 0, len(config.Rules)),
    }

    // Compile all regex patterns
    for _, rule := range config.Rules {
        regex, err := regexp.Compile(rule.Pattern)
        if err != nil {
            return nil, err
        }
        pp.rules = append(pp.rules, &compiledRule{
            pattern: regex,
            mode:    rule.Mode,
        })
    }

    return pp, nil
}

// ParsePathProtectionConfig parses path protection configuration from JSON string
func ParsePathProtectionConfig(jsonStr string) (*PathProtectionConfig, error) {
    if jsonStr == "" {
        return &PathProtectionConfig{Enabled: false}, nil
    }

    var config PathProtectionConfig
    if err := json.Unmarshal([]byte(jsonStr), &config); err != nil {
        return nil, err
    }

    config.Enabled = len(config.Rules) > 0
    return &config, nil
}

// CheckWrite checks if write operation is allowed for the given path
// Returns syscall.Errno(0) if allowed, or appropriate error if blocked
func (pp *PathProtector) CheckWrite(path string) syscall.Errno {
    if pp == nil || !pp.config.Enabled {
        return 0
    }

    pp.mu.RLock()
    defer pp.mu.RUnlock()

    for _, rule := range pp.rules {
        if rule.pattern.MatchString(path) {
            switch rule.mode {
            case ProtectionModeReadonly, ProtectionModeDeny:
                return syscall.EPERM // Operation not permitted
            }
        }
    }
    return 0
}

// CheckRead checks if read operation is allowed for the given path
// Returns syscall.Errno(0) if allowed, or appropriate error if blocked
func (pp *PathProtector) CheckRead(path string) syscall.Errno {
    if pp == nil || !pp.config.Enabled {
        return 0
    }

    pp.mu.RLock()
    defer pp.mu.RUnlock()

    for _, rule := range pp.rules {
        if rule.pattern.MatchString(path) {
            switch rule.mode {
            case ProtectionModeDeny:
                return syscall.EACCES // Permission denied
            }
        }
    }
    return 0
}

// IsProtected checks if the path matches any protection rule
func (pp *PathProtector) IsProtected(path string) bool {
    if pp == nil || !pp.config.Enabled {
        return false
    }

    pp.mu.RLock()
    defer pp.mu.RUnlock()

    for _, rule := range pp.rules {
        if rule.pattern.MatchString(path) {
            return true
        }
    }
    return false
}

// GetProtectionMode returns the protection mode for a path
// Returns empty string if not protected
func (pp *PathProtector) GetProtectionMode(path string) ProtectionMode {
    if pp == nil || !pp.config.Enabled {
        return ""
    }

    pp.mu.RLock()
    defer pp.mu.RUnlock()

    for _, rule := range pp.rules {
        if rule.pattern.MatchString(path) {
            return rule.mode
        }
    }
    return ""
}

// GetStats returns statistics about the path protector
func (pp *PathProtector) GetStats() map[string]interface{} {
    if pp == nil {
        return map[string]interface{}{"enabled": false}
    }

    pp.mu.RLock()
    defer pp.mu.RUnlock()

    rules := make([]map[string]string, 0, len(pp.rules))
    for _, r := range pp.rules {
        rules = append(rules, map[string]string{
            "pattern": r.pattern.String(),
            "mode":    string(r.mode),
        })
    }

    return map[string]interface{}{
        "enabled":    pp.config.Enabled,
        "rules":      rules,
        "mountpoint": pp.mountpoint,
    }
}

