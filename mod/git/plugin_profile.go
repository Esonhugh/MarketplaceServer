package git

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// profileDiagnostic contains only stable codes, never source text or host paths.
type profileDiagnostic string

func (d profileDiagnostic) Error() string { return ErrPluginSourceInvalid.Error() }
func (d profileDiagnostic) Unwrap() error { return ErrPluginSourceInvalid }

func validatePluginProfileV1(ctx context.Context, root string, snapshot []byte, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profileJSONValue(json.NewDecoder(bytes.NewReader(snapshot)), 0); err != nil {
		return err
	}
	manifest, err := profileJSONObject(snapshot)
	if err != nil {
		return err
	}
	for key, value := range manifest {
		switch key {
		case "name", "displayName", "version", "description", "homepage", "repository", "license":
			var text string
			if bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &text) != nil {
				return profileDiagnostic("manifest_type")
			}
			if key == "name" && (text != expected || validateSlug(text) != nil) {
				return profileDiagnostic("manifest_name")
			}
			if key == "version" && !canonicalTagPattern.MatchString("v"+text) {
				return profileDiagnostic("manifest_version")
			}
		case "defaultEnabled":
			if string(value) != "true" && string(value) != "false" {
				return profileDiagnostic("manifest_type")
			}
		case "keywords":
			if _, err := profileStrings(value); err != nil {
				return err
			}
		case "author":
			author, err := profileJSONObject(value)
			if err != nil {
				return err
			}
			for k, v := range author {
				if k != "name" && k != "email" && k != "url" {
					return profileDiagnostic("manifest_author_field")
				}
				var text string
				if string(v) == "null" || json.Unmarshal(v, &text) != nil {
					return profileDiagnostic("manifest_type")
				}
			}
		case "metadata":
			if _, err := profileJSONObject(value); err != nil {
				return err
			}
		case "skills":
			// Component paths are checked with the source tree below.
		default:
			return profileDiagnostic("manifest_unsupported_field")
		}
	}
	if _, ok := manifest["name"]; !ok {
		return profileDiagnostic("manifest_name")
	}
	return validateProfileSkills(ctx, root, manifest["skills"])
}

// Decode objects explicitly: encoding/json's struct decoder accepts case-folded
// keys and duplicate fields, neither of which is part of this profile.
func profileJSONObject(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, profileDiagnostic("manifest_encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, profileDiagnostic("manifest_json")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, profileDiagnostic("manifest_json")
		}
		key, ok := token.(string)
		if !ok {
			return nil, profileDiagnostic("manifest_json")
		}
		if _, exists := fields[key]; exists {
			return nil, profileDiagnostic("manifest_duplicate")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, profileDiagnostic("manifest_json")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, profileDiagnostic("manifest_json")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, profileDiagnostic("manifest_json")
	}
	return fields, nil
}

// Check nested opaque metadata too, without interpreting or executing its values.
func profileJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return profileDiagnostic("manifest_depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return profileDiagnostic("manifest_json")
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return profileDiagnostic("manifest_json")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return profileDiagnostic("manifest_duplicate")
			}
			seen[name] = true
		}
		if err := profileJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return profileDiagnostic("manifest_json")
	}
	return nil
}

func profileStrings(data []byte) ([]string, error) {
	var values []json.RawMessage
	if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &values) != nil {
		return nil, profileDiagnostic("manifest_type")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		var text string
		if strings.TrimSpace(string(value)) == "null" || json.Unmarshal(value, &text) != nil {
			return nil, profileDiagnostic("manifest_type")
		}
		result = append(result, text)
	}
	return result, nil
}

var profileSkillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateProfileSkills(ctx context.Context, root string, configured json.RawMessage) error {
	// Reject unvalidated conventional component entry points, even when empty.
	for _, name := range []string{"commands", "agents", "workflows", "hooks", ".mcp.json", ".lsp.json", "output-styles", "themes", "monitors", "bin", "settings", "settings.json", "SKILL.md"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			return profileDiagnostic("unsupported_component")
		}
	}
	paths := []string{"./skills"}
	if configured != nil {
		var path string
		if json.Unmarshal(configured, &path) == nil && string(configured) != "null" {
			paths = append(paths, path)
		} else {
			additional, err := profileStrings(configured)
			if err != nil {
				return err
			}
			paths = append(paths, additional...)
		}
	}
	seenPaths := map[string]bool{}
	seenNames := map[string]bool{}
	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(path, "./") || strings.Contains(path, `\`) {
			return profileDiagnostic("skill_path")
		}
		directory, err := pluginSourcePath(root, strings.TrimPrefix(path, "./"))
		if err != nil {
			return profileDiagnostic("skill_path")
		}
		// No symlink entry points: auxiliary links remain governed by materialization.
		if err := profileDirectory(root, directory); err != nil {
			if i == 0 && os.IsNotExist(err) {
				continue
			}
			return profileDiagnostic("skill_path")
		}
		if seenPaths[directory] {
			continue
		}
		seenPaths[directory] = true
		entries, err := os.ReadDir(directory)
		if err != nil {
			return profileDiagnostic("skill_path")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return profileDiagnostic("skill_path")
			}
			if !entry.IsDir() {
				return profileDiagnostic("skill_layout")
			}
			name, err := validateProfileSkill(filepath.Join(directory, entry.Name(), "SKILL.md"), entry.Name())
			if err != nil {
				return err
			}
			if seenNames[name] {
				return profileDiagnostic("skill_duplicate")
			}
			seenNames[name] = true
		}
	}
	return nil
}

func profileDirectory(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return profileDiagnostic("skill_path")
		}
	}
	return nil
}

func validateProfileSkill(path, fallback string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPluginSourceFileSize {
		return "", profileDiagnostic("skill_file")
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return "", profileDiagnostic("skill_encoding")
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", profileDiagnostic("skill_frontmatter")
	}
	end := 1
	frontmatterBytes := 0
	for end < len(lines) && lines[end] != "---" {
		frontmatterBytes += len(lines[end]) + 1
		if frontmatterBytes > 64<<10 {
			return "", profileDiagnostic("skill_frontmatter_limit")
		}
		end++
	}
	if end == len(lines) {
		return "", profileDiagnostic("skill_frontmatter")
	}
	decoder := yaml.NewDecoder(strings.NewReader(strings.Join(lines[1:end], "\n")))
	var document yaml.Node
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", profileDiagnostic("skill_yaml")
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return "", profileDiagnostic("skill_yaml")
	}
	mapping := document.Content[0]
	name := fallback
	description := false
	seen := map[string]bool{}
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Tag != "!!str" || seen[key.Value] {
			return "", profileDiagnostic("skill_field")
		}
		seen[key.Value] = true
		switch key.Value {
		case "name", "description", "argument-hint", "model", "context", "agent", "license", "compatibility":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return "", profileDiagnostic("skill_type")
			}
			if key.Value == "name" {
				name = value.Value
			}
			if key.Value == "description" {
				description = strings.TrimSpace(value.Value) != ""
			}
			if key.Value == "context" && value.Value != "fork" {
				return "", profileDiagnostic("skill_context")
			}
		case "disable-model-invocation", "user-invocable":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return "", profileDiagnostic("skill_type")
			}
		case "allowed-tools":
			if value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
				continue
			}
			if value.Kind != yaml.SequenceNode {
				return "", profileDiagnostic("skill_type")
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					return "", profileDiagnostic("skill_type")
				}
			}
		case "metadata":
			if value.Kind != yaml.MappingNode {
				return "", profileDiagnostic("skill_type")
			}
			keys := map[string]bool{}
			for j := 0; j < len(value.Content); j += 2 {
				k, v := value.Content[j], value.Content[j+1]
				if k.Tag != "!!str" || v.Tag != "!!str" || v.Kind != yaml.ScalarNode || keys[k.Value] {
					return "", profileDiagnostic("skill_type")
				}
				keys[k.Value] = true
			}
		default:
			return "", profileDiagnostic("skill_unsupported_field")
		}
	}
	if !description || len(name) > 64 || !profileSkillName.MatchString(name) {
		return "", profileDiagnostic("skill_identity")
	}
	return name, nil
}
