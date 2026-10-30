import { getProjects, createProject } from "../lib/fleet";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Loader2 } from "lucide-react";
import { Badge, Button, Dialog, DialogContent, DialogHeader, DialogTitle, Label } from "../components/ui";
import { LoadingRows, EmptyState, ErrorState } from "../components/ui";
import type { FormEvent } from "react";

export function ProjectsScreen() {
  const [toast, setToast] = useState<{ title: string; description: string; variant?: "default" | "destructive" } | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formData, setFormData] = useState({
    key: "",
    name: "",
    forge: "github" as "github" | "gitlab",
    repo: "",
    default_branch: "main",
    rigs: [] as string[],
    concurrency_cap: 2,
    paths: { main: "", worktrees: "" },
    rig_template: "default",
    labels: {},
    onhold: false,
    gate_cmd: "",
  });
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  const { data: projectsResponse, isLoading, error, refetch } = useQuery({
    queryKey: ["projects"],
    queryFn: getProjects,
  });

  const handleFieldChange = (field: string, value: string | number | boolean | string[]) => {
    setFormData((prev) => ({ ...prev, [field]: value }));
    if (fieldErrors[field]) {
      setFieldErrors((prev) => ({ ...prev, [field]: "" }));
    }
  };

  const validateForm = (): boolean => {
    const errors: Record<string, string> = {};
    if (!formData.key) errors.key = "Key is required";
    else if (!/^[a-z0-9-]+$/.test(formData.key)) errors.key = "Must be lowercase alphanumeric with hyphens";
    if (!formData.name) errors.name = "Name is required";
    if (!formData.forge) errors.forge = "Forge is required";
    if (!formData.repo) errors.repo = "Repo is required (owner/name)";
    if (!formData.paths.main) errors["paths.main"] = "paths.main is required";
    if (formData.rigs.length === 0) errors.rigs = "At least one rig is required";
    if (formData.concurrency_cap < 1) errors.concurrency_cap = "Must be at least 1";
    setFieldErrors(errors);
    return Object.keys(errors).length === 0;
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!validateForm()) return;
    setSubmitting(true);
    try {
      const response = await createProject(formData);
      setToast({
        title: "Project created",
        description: `${response.project.name} (${response.project.key}) added`,
      });
      if (response.warnings && response.warnings.length > 0) {
        setTimeout(() => {
          setToast({
            title: "Warning",
            description: response.warnings.join("; "),
            variant: "destructive",
          });
        }, 500);
      }
      setDialogOpen(false);
      setFormData({
        key: "",
        name: "",
        forge: "github",
        repo: "",
        default_branch: "main",
        rigs: [],
        concurrency_cap: 2,
        paths: { main: "", worktrees: "" },
        rig_template: "default",
        labels: {},
        onhold: false,
        gate_cmd: "",
      });
      refetch();
    } catch (err: unknown) {
      if (err && typeof err === "object" && "status" in err && "field_errors" in err) {
        const apiErr = err as unknown as { field_errors: Record<string, string>; message: string };
        setFieldErrors(apiErr.field_errors);
      } else {
        setToast({ title: "Error", description: "Failed to create project", variant: "destructive" });
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-semibold">Projects</h1>
        <Button onClick={() => setDialogOpen(true)} className="gap-2">
          <Plus className="h-4 w-4" />
          New project
        </Button>
      </div>

      {isLoading ? (
        <LoadingRows rows={5} />
      ) : error ? (
        <ErrorState
          title="Failed to load projects"
          error={error}
          onRetry={() => refetch()}
        />
      ) : !projectsResponse?.projects?.length ? (
        <EmptyState
          title="No projects registered"
          body="The project registry is empty. Add your first project to start dispatching work."
          action={
            <Button onClick={() => setDialogOpen(true)} className="gap-2">
              <Plus className="h-4 w-4" />
              Add first project
            </Button>
          }
        />
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full" role="table">
            <thead>
              <tr className="border-b border-rule-strong bg-muted/50">
                <th className="px-4 py-3 text-left font-medium">Key</th>
                <th className="px-4 py-3 text-left font-medium">Name</th>
                <th className="px-4 py-3 text-left font-medium">Forge</th>
                <th className="px-4 py-3 text-left font-medium">Repo</th>
                <th className="px-4 py-3 text-left font-medium">Rigs</th>
                <th className="px-4 py-3 text-right font-medium">Cap</th>
              </tr>
            </thead>
            <tbody>
              {projectsResponse?.projects?.map((project) => (
                <tr key={project.key} className="border-b border-rule">
                  <td className="px-4 py-3 font-mono">{project.key}</td>
                  <td className="px-4 py-3">{project.name}</td>
                  <td className="px-4 py-3">
                    <Badge tone={project.forge === "github" ? "quiet" : "tint"}>
                      {project.forge}
                    </Badge>
                  </td>
                  <td className="px-4 py-3 font-mono text-sm">{project.repo}</td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {project.rigs.map((rig) => (
                        <Badge key={rig} tone="quiet" className="text-xs">
                          {rig}
                        </Badge>
                      ))}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-right font-mono">{project.concurrency_cap}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleSubmit} className="space-y-4 p-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="key">Key *</Label>
                <input
                  id="key"
                  type="text"
                  value={formData.key}
                  onChange={(e) => handleFieldChange("key", e.target.value)}
                  placeholder="my-project"
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.key}
                />
                {fieldErrors.key && <p className="text-sm text-destructive">{fieldErrors.key}</p>}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="name">Name *</Label>
                <input
                  id="name"
                  type="text"
                  value={formData.name}
                  onChange={(e) => handleFieldChange("name", e.target.value)}
                  placeholder="My Project"
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.name}
                />
                {fieldErrors.name && <p className="text-sm text-destructive">{fieldErrors.name}</p>}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="forge">Forge *</Label>
                <select
                  id="forge"
                  value={formData.forge}
                  onChange={(e) => handleFieldChange("forge", e.target.value as "github" | "gitlab")}
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.forge}
                >
                  <option value="github">GitHub</option>
                  <option value="gitlab">GitLab</option>
                </select>
                {fieldErrors.forge && <p className="text-sm text-destructive">{fieldErrors.forge}</p>}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="repo">Repo *</Label>
                <input
                  id="repo"
                  type="text"
                  value={formData.repo}
                  onChange={(e) => handleFieldChange("repo", e.target.value)}
                  placeholder="owner/name"
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.repo}
                />
                {fieldErrors.repo && <p className="text-sm text-destructive">{fieldErrors.repo}</p>}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="default_branch">Default branch</Label>
                <input
                  id="default_branch"
                  type="text"
                  value={formData.default_branch}
                  onChange={(e) => handleFieldChange("default_branch", e.target.value)}
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="concurrency_cap">Concurrency cap *</Label>
                <input
                  id="concurrency_cap"
                  type="number"
                  min="1"
                  value={formData.concurrency_cap}
                  onChange={(e) => handleFieldChange("concurrency_cap", parseInt(e.target.value) || 0)}
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.concurrency_cap}
                />
                {fieldErrors.concurrency_cap && <p className="text-sm text-destructive">{fieldErrors.concurrency_cap}</p>}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="rigs">Rigs (comma-separated) *</Label>
                <input
                  id="rigs"
                  type="text"
                  value={formData.rigs.join(", ")}
                  onChange={(e) => handleFieldChange("rigs", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
                  placeholder="fleet-console, ghost"
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  aria-invalid={!!fieldErrors.rigs}
                />
                {fieldErrors.rigs && <p className="text-sm text-destructive">{fieldErrors.rigs}</p>}
                <p className="text-xs text-muted-foreground">Comma-separated list of rig names</p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="rig_template">Rig template</Label>
                <input
                  id="rig_template"
                  type="text"
                  value={formData.rig_template}
                  onChange={(e) => handleFieldChange("rig_template", e.target.value)}
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                />
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="paths.main">paths.main *</Label>
              <input
                id="paths.main"
                type="text"
                value={formData.paths.main}
                onChange={(e) => handleFieldChange("paths.main", e.target.value)}
                placeholder="~/src/my-project"
                disabled={submitting}
                className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                aria-invalid={!!fieldErrors["paths.main"]}
              />
              {fieldErrors["paths.main"] && <p className="text-sm text-destructive">{fieldErrors["paths.main"]}</p>}
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="paths.worktrees">paths.worktrees</Label>
              <input
                id="paths.worktrees"
                type="text"
                value={formData.paths.worktrees}
                onChange={(e) => handleFieldChange("paths.worktrees", e.target.value)}
                placeholder="~/src/worktrees"
                disabled={submitting}
                className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="gate_cmd">Gate command</Label>
                <input
                  id="gate_cmd"
                  type="text"
                  value={formData.gate_cmd}
                  onChange={(e) => handleFieldChange("gate_cmd", e.target.value)}
                  placeholder="scripts/gate.sh"
                  disabled={submitting}
                  className="flex h-8 w-full rounded-chip border border-input bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                />
              </div>
              <div className="space-y-1.5 flex items-end">
                <Label htmlFor="onhold">On hold</Label>
                <input
                  id="onhold"
                  type="checkbox"
                  checked={formData.onhold}
                  onChange={(e) => handleFieldChange("onhold", e.target.checked)}
                  disabled={submitting}
                  className="peer h-4 w-4 shrink-0 rounded-chip border border-input"
                />
              </div>
            </div>

            <div className="flex justify-end gap-2 pt-4 border-t">
              <button
                type="button"
                onClick={() => setDialogOpen(false)}
                disabled={submitting}
                className="inline-flex items-center justify-center gap-2 rounded-chip border border-input bg-background px-4 py-2 text-sm font-medium hover:bg-accent hover:text-accent-foreground disabled:cursor-not-allowed disabled:opacity-50"
              >
                Cancel
              </button>
              <button type="submit" disabled={submitting} className="inline-flex items-center justify-center gap-2 rounded-chip bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50">
                {submitting && <Loader2 className="h-4 w-4 animate-spin mr-2" />}
                Create project
              </button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
        {toast && (
          <div className={`flex items-center gap-2 rounded-lg border bg-background p-4 shadow-lg ${toast.variant === "destructive" ? "border-destructive" : "border-border"}`}>
            <div className="flex-1">
              <p className="font-medium">{toast.title}</p>
              <p className="text-sm text-muted-foreground">{toast.description}</p>
            </div>
            <button onClick={() => setToast(null)} className="text-muted-foreground hover:text-foreground">×</button>
          </div>
        )}
      </div>
    </div>
  );

}