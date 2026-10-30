package rig

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/rig.yaml.tmpl
var templateText string

var rigTemplate = template.Must(template.New("rig.yaml").Funcs(template.FuncMap{
	"quote": strconv.Quote,
}).Parse(templateText))

// EnvVar is one account environment entry. A slice, not a map, keeps the
// rendered order stable for the golden file.
type EnvVar struct {
	Name  string
	Value string
}

// StartupFile is a file delivered to a seat after launch.
type StartupFile struct {
	Path     string
	Required bool
}

// Edge is one pod edge.
type Edge struct {
	Kind string
	From string
	To   string
}

// Member is one declared seat.
type Member struct {
	ID           string
	AgentRef     string
	Runtime      string
	Model        string
	Profile      string
	Env          []EnvVar
	Cwd          string
	StartupFiles []StartupFile
}

// Pod is one group of seats.
type Pod struct {
	ID      string
	Label   string
	Members []Member
	Edges   []Edge
}

// RigSpec is the typed form of rig.yaml.
type RigSpec struct {
	Name             string
	Summary          string
	CultureFile      string
	PermissionPolicy string
	Pods             []Pod
}

// Project is the row the renderer reads. Everything the rig needs is explicit;
// nothing is read from the live topology.
type Project struct {
	Key            string
	Rig            string
	Name           string
	Summary        string
	RepoPath       string
	WorktreeRoot   string
	Builders       int
	ReviewerCwd    string
	Runtime        string
	BuilderModel   string
	ReviewerModel  string
	ImplementerRef string
	ReviewerRef    string
	ProviderEnv    string
	ProviderKey    string
	CulturePath    string
	GuidancePath   string
}

const (
	defaultImplementerRef = "local:./agents/development/implementer"
	defaultReviewerRef    = "local:./agents/development/qa"
	defaultPermission     = "builtin:yolo"
)

// Members returns the rendered seat list. It is the single place seat ids and
// session names are derived.
func (r *RigSpec) Members() []Member {
	var members []Member
	for _, pod := range r.Pods {
		members = append(members, pod.Members...)
	}
	return members
}

// SessionName derives the OpenRig canonical session for a seat: pod-member@rig.
func (r *RigSpec) SessionName(podID, memberID string) string {
	return podID + "-" + memberID + "@" + r.Name
}

// RenderRig turns a project row into a spec and its YAML. It refuses to render
// a spec without a provider credential, because a rig that cannot start its
// seats is worse than a refused launch.
func RenderRig(p Project) (*RigSpec, []byte, error) {
	if strings.TrimSpace(p.Rig) == "" {
		return nil, nil, fmt.Errorf("render: rig name is required")
	}
	if strings.TrimSpace(p.Key) == "" {
		return nil, nil, fmt.Errorf("render: project key is required")
	}
	if p.Builders < 1 {
		return nil, nil, fmt.Errorf("render: builder count must be at least 1")
	}
	if strings.TrimSpace(p.ProviderEnv) == "" {
		return nil, nil, fmt.Errorf("render: provider environment is required")
	}
	if strings.TrimSpace(p.CulturePath) == "" {
		return nil, nil, fmt.Errorf("render: culture file is required")
	}
	runtime := p.Runtime
	if strings.TrimSpace(runtime) == "" {
		runtime = DefaultRuntime
	} else if canonical, ok := KnownRuntime(runtime); ok {
		runtime = canonical
	} else {
		return nil, nil, fmt.Errorf("render: runtime %q is not one of %s", runtime, RuntimesText)
	}
	builderModel := p.BuilderModel
	if builderModel == "" {
		return nil, nil, fmt.Errorf("render: builder model is required")
	}
	reviewerModel := p.ReviewerModel
	if reviewerModel == "" {
		reviewerModel = builderModel
	}
	implementer := p.ImplementerRef
	if implementer == "" {
		implementer = defaultImplementerRef
	}
	reviewer := p.ReviewerRef
	if reviewer == "" {
		reviewer = defaultReviewerRef
	}
	reviewerCwd := p.ReviewerCwd
	if reviewerCwd == "" {
		reviewerCwd = p.RepoPath
	}
	if strings.TrimSpace(reviewerCwd) == "" {
		return nil, nil, fmt.Errorf("render: reviewer cwd is required")
	}

	guidance := ""
	if p.GuidancePath != "" {
		guidance = filepath.Base(p.GuidancePath)
	}
	startup := func() []StartupFile {
		if guidance == "" {
			return nil
		}
		return []StartupFile{{Path: "guidance/" + guidance, Required: true}}
	}

	members := make([]Member, 0, p.Builders+1)
	edges := make([]Edge, 0, p.Builders)
	for i := 1; i <= p.Builders; i++ {
		cwd := filepath.Join(p.WorktreeRoot, fmt.Sprintf("wt-%s-%d", p.Key, i))
		if strings.TrimSpace(p.WorktreeRoot) == "" {
			return nil, nil, fmt.Errorf("render: worktree root is required for %d builders", p.Builders)
		}
		id := fmt.Sprintf("build-%d", i)
		members = append(members, Member{
			ID:           id,
			AgentRef:     implementer,
			Runtime:      runtime,
			Model:        builderModel,
			Profile:      "default",
			Env:          []EnvVar{{Name: p.ProviderEnv, Value: p.ProviderKey}},
			Cwd:          cwd,
			StartupFiles: startup(),
		})
		edges = append(edges, Edge{Kind: "delegates_to", From: id, To: "review"})
	}
	members = append(members, Member{
		ID:           "review",
		AgentRef:     reviewer,
		Runtime:      runtime,
		Model:        reviewerModel,
		Profile:      "default",
		Env:          []EnvVar{{Name: p.ProviderEnv, Value: p.ProviderKey}},
		Cwd:          reviewerCwd,
		StartupFiles: startup(),
	})

	spec := &RigSpec{
		Name:             p.Rig,
		Summary:          p.Summary,
		CultureFile:      "CULTURE.md",
		PermissionPolicy: defaultPermission,
		Pods: []Pod{{
			ID:      "dev",
			Label:   "Issues",
			Members: members,
			Edges:   edges,
		}},
	}
	var out strings.Builder
	if err := rigTemplate.Execute(&out, spec); err != nil {
		return nil, nil, fmt.Errorf("render rig %s: %w", p.Rig, err)
	}
	return spec, []byte(out.String()), nil
}

// WriteSpecDir writes the rendered spec, culture file, guidance and agents
// symlink under <home>/.openrig/specs/rigs/<rig>. It never edits a spec by hand.
func WriteSpecDir(home string, spec *RigSpec, rendered []byte, culturePath, guidancePath, agentsDir string) (string, error) {
	if home == "" {
		return "", fmt.Errorf("write spec: home is required")
	}
	if spec == nil || spec.Name == "" {
		return "", fmt.Errorf("write spec: spec name is required")
	}
	dir := filepath.Join(home, ".openrig", "specs", "rigs", spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("write spec: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "rig.yaml"), rendered, 0o600); err != nil {
		return "", err
	}
	if culturePath != "" {
		culture, err := os.ReadFile(culturePath)
		if err != nil {
			return "", fmt.Errorf("write spec: read culture: %w", err)
		}
		if err := writeFileAtomic(filepath.Join(dir, "CULTURE.md"), culture, 0o644); err != nil {
			return "", err
		}
	}
	if guidancePath != "" {
		guidance, err := os.ReadFile(guidancePath)
		if err != nil {
			return "", fmt.Errorf("write spec: read guidance: %w", err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "guidance"), 0o755); err != nil {
			return "", fmt.Errorf("write spec: %w", err)
		}
		if err := writeFileAtomic(filepath.Join(dir, "guidance", filepath.Base(guidancePath)), guidance, 0o644); err != nil {
			return "", err
		}
	}
	if agentsDir != "" {
		link := filepath.Join(dir, "agents")
		if target, err := os.Readlink(link); err != nil || target != agentsDir {
			_ = os.Remove(link)
			if err := os.Symlink(agentsDir, link); err != nil {
				return "", fmt.Errorf("write spec: agents symlink: %w", err)
			}
		}
	}
	return dir, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
