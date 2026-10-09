package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SetUI writes one ui: option into the config file at path, through the
// YAML tree so comments and the other keys stay. A nil value removes the
// option (back to its default). A list is written in flow style.
func SetUI(path, name string, value any) error {
	return editConfig(path, false, false, func(root *yaml.Node) error {
		ui, err := section(root, "ui", value != nil)
		if err != nil || ui == nil {
			return err
		}
		return setMapping(ui, name, value)
	})
}

// SetSite writes a Jira site into the config file at path: jira: for
// name "", else sites.<name>. Its base_url, email and api_token replace
// the site's (an empty token is left out, for JIRA_API_TOKEN); its other
// keys and every comment stay. A missing file is created. Writing a
// token makes the file readable by its owner only.
func SetSite(path, name string, j JiraConfig) error {
	return editConfig(path, true, j.APIToken != "", func(root *yaml.Node) error {
		site, err := section(root, "jira", true)
		if name != "" {
			var sites *yaml.Node
			if sites, err = section(root, "sites", true); err == nil {
				site, err = section(sites, name, true)
			}
		}
		if err != nil {
			return err
		}
		for _, f := range []struct{ key, v string }{{"base_url", j.BaseURL}, {"email", j.Email}, {"api_token", j.APIToken}} {
			if f.v == "" {
				continue
			}
			if err := setMapping(site, f.key, f.v); err != nil {
				return err
			}
		}
		if j.LanewaySignIn() { // the sign-in is kept apart: no token at all
			for _, k := range []string{"api_token", "api_token_cmd"} {
				if err := setMapping(site, k, nil); err != nil {
					return err
				}
			}
			return setMapping(site, "sign_in", SignInLaneway)
		}
		if err := setMapping(site, "sign_in", nil); err != nil { // a token again
			return err
		}
		if len(j.APITokenCmd) > 0 { // the keyring holds it: no token in the file
			if err := setMapping(site, "api_token", nil); err != nil {
				return err
			}
			return setMapping(site, "api_token_cmd", j.APITokenCmd)
		}
		return nil
	})
}

// SetProjects writes a site's projects (jira: for name "", else
// sites.<name>) into the config file at path; none removes the key.
func SetProjects(path, name string, projects []string) error {
	return editConfig(path, false, false, func(root *yaml.Node) error {
		site, err := section(root, "jira", true)
		if name != "" {
			var sites *yaml.Node
			if sites, err = section(root, "sites", true); err == nil {
				site, err = section(sites, name, true)
			}
		}
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			return setMapping(site, "projects", nil)
		}
		return setMapping(site, "projects", projects)
	})
}

// editConfig applies edit to the config file at path through its YAML
// tree, so comments and the other keys stay. create makes a missing file
// (and its directory), else a missing file is an error. private drops
// the file's group and other permissions.
func editConfig(path string, create, private bool, edit func(root *yaml.Node) error) error {
	// A dotfiles symlink stays one: write where it points.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && create {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		raw, err = nil, nil
	}
	if err != nil {
		return err
	}
	// Every document is kept; the first is the config.
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		docs = append(docs, &doc)
	}
	var buf bytes.Buffer
	if len(docs) == 0 {
		// Only comments, which the YAML tree doesn't hold: keep them as text.
		buf.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			buf.WriteByte('\n')
		}
		docs = []*yaml.Node{{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}}
	}
	root := docs[0].Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" { // a bare "---"
		root.Kind, root.Tag, root.Value = yaml.MappingNode, "", ""
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a mapping", path)
	}
	if err := edit(root); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return err
		}
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes(), private)
}

// section is key's mapping in m. A missing one is added when add, else
// nil; a bare "key:" becomes an empty mapping.
func section(m *yaml.Node, key string, add bool) (*yaml.Node, error) {
	v := mappingValue(m, key)
	switch {
	case v == nil && !add:
		return nil, nil
	case v == nil:
		v = &yaml.Node{Kind: yaml.MappingNode}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	case v.Kind == yaml.ScalarNode && v.Tag == "!!null":
		v.Kind, v.Tag, v.Value = yaml.MappingNode, "", ""
	case v.Kind != yaml.MappingNode:
		return nil, fmt.Errorf("%s: is not a mapping", key)
	}
	return v, nil
}

// mappingValue is key's value node in m, nil when m lacks it.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapping sets or (nil) removes key in m, keeping a replaced value's
// line comment.
func setMapping(m *yaml.Node, key string, value any) error {
	at := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			at = i
		}
	}
	if value == nil {
		if at >= 0 {
			m.Content = append(m.Content[:at], m.Content[at+2:]...)
		}
		return nil
	}
	var v yaml.Node
	if err := v.Encode(value); err != nil {
		return err
	}
	if v.Kind == yaml.SequenceNode {
		v.Style = yaml.FlowStyle
	}
	if at < 0 {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &v)
		return nil
	}
	v.LineComment = m.Content[at+1].LineComment
	m.Content[at+1] = &v
	return nil
}

// writeFile replaces path through a temporary file beside it, keeping its
// mode (less group and other when private), so a crash never leaves
// half a config.
func writeFile(path string, data []byte, private bool) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if private {
		mode &^= 0o077
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
