package marketplacejson

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const URLSourceType = "url"

var (
	kebabCasePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Marketplace struct {
	Name        string   `json:"name"`
	Owner       Owner    `json:"owner"`
	Plugins     []Plugin `json:"plugins"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
}

type Owner struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Plugin struct {
	Name        string    `json:"name"`
	Source      URLSource `json:"source"`
	Description string    `json:"description,omitempty"`
	Version     string    `json:"version,omitempty"`
}

type URLSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	SHA    string `json:"sha,omitempty"`
}

func Marshal(marketplace Marketplace) ([]byte, error) {
	if err := Validate(marketplace); err != nil {
		return nil, err
	}
	return json.Marshal(marketplace)
}

func Validate(marketplace Marketplace) error {
	if !kebabCasePattern.MatchString(marketplace.Name) {
		return errors.New("marketplace name must be non-empty kebab-case")
	}
	if strings.TrimSpace(marketplace.Owner.Name) == "" {
		return errors.New("marketplace owner name is required")
	}
	if marketplace.Plugins == nil {
		return errors.New("marketplace plugins must be an array")
	}

	seen := make(map[string]struct{}, len(marketplace.Plugins))
	for i, plugin := range marketplace.Plugins {
		if strings.TrimSpace(plugin.Name) == "" {
			return fmt.Errorf("plugin %d name is required", i)
		}
		if _, exists := seen[plugin.Name]; exists {
			return fmt.Errorf("plugin name %q is duplicated", plugin.Name)
		}
		seen[plugin.Name] = struct{}{}
		if plugin.Source.Source != URLSourceType {
			return fmt.Errorf("plugin %q source must be %q", plugin.Name, URLSourceType)
		}
		if strings.TrimSpace(plugin.Source.URL) == "" {
			return fmt.Errorf("plugin %q source URL is required", plugin.Name)
		}
		if strings.TrimSpace(plugin.Source.Ref) == "" {
			return fmt.Errorf("plugin %q source ref is required", plugin.Name)
		}
		if !commitSHAPattern.MatchString(plugin.Source.SHA) {
			return fmt.Errorf("plugin %q source SHA must be a full lowercase commit SHA", plugin.Name)
		}
	}
	return nil
}
