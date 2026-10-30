package configfile

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Outcome is the result of an upsert.
type Outcome string

const (
	OutcomeCreated  Outcome = "Created"
	OutcomeUnchanged Outcome = "Unchanged"
)

// UpsertProject writes a project to the config file at path.
// expectedHash is the sha256 of the file content the caller read; if provided
// and the file on disk differs, the write is refused. The write is atomic:
// temp file in the same dir, fsync, original mode, rename.
func UpsertProject(path string, in Project, expectedHash *[32]byte) (Outcome, error) {
	// Read the current file content
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte("")
		} else {
			return "", fmt.Errorf("configfile: read %s: %w", path, err)
		}
	}

	// If expectedHash provided, verify file hasn't changed since the caller read it
	if expectedHash != nil {
		nowHash := sha256.Sum256(data)
		if nowHash != *expectedHash {
			return "", fmt.Errorf("config.yaml changed on disk; reload and retry")
		}
	}



	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return "", fmt.Errorf("configfile: unmarshal %s: %w", path, err)
		}
		// Unmarshal returns a document node; get the mapping inside
		if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
			root = *root.Content[0]
		}
	} else {
		root = yaml.Node{Kind: yaml.MappingNode}
	}

	// Find or create the projects sequence
	var projectsNode *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "projects" {
			projectsNode = root.Content[i+1]
			break
		}
	}

	if projectsNode == nil {
		// Create projects: [] sequence
		projectsNode = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "projects"},
			projectsNode)
	} else if projectsNode.Kind != yaml.SequenceNode {
		return "", fmt.Errorf("configfile: projects is not a sequence")
	}

	// Check for existing key
	for _, item := range projectsNode.Content {
		if item.Kind == yaml.MappingNode {
			for j := 0; j < len(item.Content); j += 2 {
				if item.Content[j].Value == "key" && item.Content[j+1].Value == in.Key {
					// Build a node from in to compare byte-for-byte
					newNode, err := projectToNode(in)
					if err != nil {
						return "", err
					}
					if nodesEqual(item, newNode) {
						return OutcomeUnchanged, nil
					}
					return "", fmt.Errorf("duplicate project key %q", in.Key)
				}
			}
		}
	}

	// Append the new project node
	newNode, err := projectToNode(in)
	if err != nil {
		return "", err
	}
	projectsNode.Content = append(projectsNode.Content, newNode)

	// Marshal the whole document
	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("configfile: marshal: %w", err)
	}



	// Atomic write: temp file in same dir, fsync, original mode, rename
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "config.*.yaml.tmp")
	if err != nil {
		return "", fmt.Errorf("configfile: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Ensure cleanup on error
	defer func() {
		os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return "", fmt.Errorf("configfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("configfile: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("configfile: close temp: %w", err)
	}

	// Preserve original mode if file existed
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return "", fmt.Errorf("configfile: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("configfile: rename: %w", err)
	}

	return OutcomeCreated, nil
}

// Project is the subset of config.Project that the file stores.
type Project struct {
	Key            string            `yaml:"key"`
	Name           string            `yaml:"name"`
	Forge          string            `yaml:"forge"`
	Repo           string            `yaml:"repo"`
	DefaultBranch  string            `yaml:"default_branch"`
	Rigs           []string          `yaml:"rigs"`
	RigTemplate    string            `yaml:"rig_template"`
	ConcurrencyCap int               `yaml:"concurrency_cap"`
	GateCmd        string            `yaml:"gate_cmd"`
	Labels         map[string]string `yaml:"labels"`
	OnHold         *bool             `yaml:"onhold"`
	Paths          struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}

// projectToNode converts a Project to a yaml.Node (mapping) with proper ordering.
func projectToNode(p Project) (*yaml.Node, error) {
	// Marshal to intermediate to get a node with correct tags
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	// The unmarshaled node is a document node; we need the mapping inside
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0], nil
	}
	return &node, nil
}

// nodesEqual checks if two mapping nodes have the same keys and values in order.
func nodesEqual(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || len(a.Content) != len(b.Content) {
		return false
	}
	for i := 0; i < len(a.Content); i++ {
		if a.Content[i].Value != b.Content[i].Value {
			return false
		}
	}
	return true
}