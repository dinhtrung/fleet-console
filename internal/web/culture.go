package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/culture"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// CultureResponse is the read body (design §2). Content is present when the file
// exists; a missing file is a 200 with exists=false, never a 404, because the
// screen needs the path in order to explain itself.
type CultureResponse struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	Rig      string `json:"rig"`
	Exists   bool   `json:"exists"`
	Writable bool   `json:"writable"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	MTime    string `json:"mtime,omitempty"`
	Content  string `json:"content,omitempty"`
}

// CultureWriteRequest is the put body. It has exactly two fields and no path: a
// body that names a file is ignored, because a request must never steer where
// the app writes (design §1).
type CultureWriteRequest struct {
	Content string  `json:"content"`
	BaseSHA *string `json:"base_sha"`
}

// CultureWriteResponse is the success body (design §3).
type CultureWriteResponse struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	MTime  string `json:"mtime"`
	Backup string `json:"backup,omitempty"`
}

// RegisterCultureRoutes registers GET and PUT /api/projects/{key}/culture inside
// the authenticated API group. cfg.Store resolves the project row; cfg.ConfigPath
// is the file whose projects list says where a culture file lives.
func RegisterCultureRoutes(api fiber.Router, cfg Config, configPath string) {
	api.Get("/projects/:key/culture", readCulture(cfg.Store, configPath))
	api.Put("/projects/:key/culture", writeCulture(cfg.Store, configPath))
}

// cultureTarget resolves a project key to its culture target, answering the two
// failures a key can have: unknown project (404) and a project with no culture
// file configured (a body, not a failure — the screen explains it).
func cultureTarget(ctx context.Context, st *store.Store, configPath, key string) (culture.Target, config.Project, error) {
	if st == nil {
		return culture.Target{}, config.Project{}, fiber.NewError(fiber.StatusServiceUnavailable, "store is not configured")
	}
	row, err := st.ProjectByKey(ctx, key)
	if err != nil {
		return culture.Target{}, config.Project{}, fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if row == nil {
		return culture.Target{}, config.Project{}, fiber.NewError(fiber.StatusNotFound, "project "+key+" is not registered")
	}
	eff, err := config.Load(config.Options{FlagPath: configPath})
	if err != nil {
		return culture.Target{}, config.Project{}, fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	project, found := findConfiguredProject(eff.Projects, key)
	if !found {
		// Registered in the store but absent from the file: the file is the
		// source of truth for where the culture lives, so there is nothing to
		// resolve against.
		return culture.Target{Source: culture.SourceNotConfigured}, config.Project{Key: key}, nil
	}
	target := culture.Resolve(
		culture.Project{CultureFile: project.CultureFile, Rigs: project.Rigs},
		culture.SpecsDir(""),
	)
	return target, project, nil
}

func findConfiguredProject(projects []config.Project, key string) (config.Project, bool) {
	for _, project := range projects {
		if project.Key == key {
			return project, true
		}
	}
	return config.Project{}, false
}

func readCulture(st *store.Store, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		target, _, err := cultureTarget(c.Context(), st, configPath, c.Params("key"))
		if err != nil {
			return err
		}
		// not_configured is an answer, not a failure: the body carries the path
		// the operator would need to create, which is what the screen shows.
		if target.Source == culture.SourceNotConfigured {
			return c.JSON(CultureResponse{
				Path: target.Path, Source: string(target.Source), Rig: target.Rig,
			})
		}
		snapshot, err := culture.Read(target)
		if err != nil {
			return cultureFailure(c, err, false)
		}
		return c.JSON(CultureResponse{
			Path: target.Path, Source: string(target.Source), Rig: target.Rig,
			Exists: snapshot.Exists, Writable: snapshot.Writable, Bytes: snapshot.Bytes,
			SHA256: snapshot.SHA256, MTime: formatMTime(snapshot.MTime), Content: snapshot.Content,
		})
	}
}

func writeCulture(st *store.Store, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		target, _, err := cultureTarget(c.Context(), st, configPath, c.Params("key"))
		if err != nil {
			return err
		}
		var req CultureWriteRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}
		if target.Source == culture.SourceNotConfigured {
			return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
				Error: "no culture file is configured for this project",
			})
		}

		// The store owns the audit trail, so the handler reads the old sha
		// before the write and records the event after it.
		oldSHA := ""
		if snapshot, readErr := culture.Read(target); readErr == nil {
			oldSHA = snapshot.SHA256
		}

		result, err := culture.Write(target, req.Content, req.BaseSHA)
		if err != nil {
			return cultureFailure(c, err, true)
		}

		if st != nil {
			if _, err := st.AppendEvent(c.Context(), store.Event{
				Actor: "board", Kind: "culture.edited", Subject: target.Path,
				Detail: cultureEventDetail(oldSHA, result.SHA256, result.Bytes, target.Path),
			}); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
		}

		return c.JSON(CultureWriteResponse{
			SHA256: result.SHA256, Bytes: result.Bytes,
			MTime: formatMTime(result.MTime), Backup: result.Backup,
		})
	}
}

// cultureFailure maps a typed culture error onto the status design §3 assigns.
// It is one place because the read and the write share four of the six rows, and
// two mappings of one table is how a status drifts.
func cultureFailure(c fiber.Ctx, err error, writing bool) error {
	var size *culture.SizeError
	if errors.As(err, &size) {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": "content too large", "limit": size.Limit,
		})
	}
	if errors.Is(err, culture.ErrNotText) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{Error: "content must be UTF-8 text"})
	}
	var stale *culture.StaleError
	if errors.As(err, &stale) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "stale", "sha": stale.SHA, "mtime": formatMTime(stale.MTime),
		})
	}
	if errors.Is(err, culture.ErrNoFile) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "no culture file", "sha": nil})
	}
	var notWritable *culture.NotWritableError
	if errors.As(err, &notWritable) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "not writable", "path": notWritable.Path,
			"mode": formatMode(notWritable.Mode),
		})
	}
	var refused *culture.RefusedError
	if errors.As(err, &refused) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "refused", "path": refused.Path, "resolved": refused.Resolved,
		})
	}
	if errors.Is(err, culture.ErrNotConfigured) {
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{Error: err.Error()})
	}
	if writing {
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
	}
	return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
}

func cultureEventDetail(oldSHA, newSHA string, bytes int64, path string) string {
	return "old_sha=" + oldSHA + " new_sha=" + newSHA + " bytes=" + fmt.Sprint(bytes) + " path=" + path
}

func formatMTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

func formatMode(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}
