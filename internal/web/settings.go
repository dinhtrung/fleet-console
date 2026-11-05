package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
)

// SettingsResponse is GET /api/config. Every field is either a setting the
// surface may write or a read-only fact the screen needs to explain itself.
// source names where each value came from; it is the same map --print-config
// prints, so the screen and the CLI cannot disagree.
type SettingsResponse struct {
	Settings map[string]string `json:"settings"`
	Source   map[string]string `json:"source"`
	// ReadOnly lists the keys the surface refuses, each with its reason, so the
	// screen can render them disabled with a real explanation instead of hiding
	// them or guessing why.
	ReadOnly []ReadOnlyKey `json:"read_only"`
	// Hash is the sha256 of the file bytes this read came from. A save sends it
	// back; a mismatch means the file moved and the save is refused with 409.
	Hash string `json:"hash"`
}

// ReadOnlyKey is one refused key and the reason the surface refuses it.
type ReadOnlyKey struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// SaveSettingsResponse is PUT /api/config. saved is false when the values
// already matched the file; restart_required names the unit command's
// consequence without running it (design D4).
type SaveSettingsResponse struct {
	Saved           bool     `json:"saved"`
	Changed         []string `json:"changed"`
	RestartRequired bool     `json:"restart_required"`
	// Settings and Source are the values after the write, so a save answers with
	// the same shape a read does and the screen needs no second request.
	Settings map[string]string `json:"settings"`
	Source   map[string]string `json:"source"`
	// Hash is the new file hash, so a second save from the same screen is a real
	// read-modify-write rather than a stale one.
	Hash string `json:"hash"`
}

// SaveSettingsRequest is the PUT body. A key absent from values is left alone;
// only the keys present are written.
type SaveSettingsRequest struct {
	Values map[string]string `json:"values"`
	Hash   string            `json:"hash"`
}

// RegisterSettingsRoutes registers GET and PUT /api/config inside the
// authenticated API group. configPath may be empty on a box with no file; the
// read still answers from the effective config and the write answers 500.
func RegisterSettingsRoutes(api fiber.Router, cfg Config, configPath string) {
	write := cfg.WriteSettings
	if write == nil {
		write = configfile.UpdateSettings
	}
	api.Get("/config", readSettings(configPath))
	api.Put("/config", saveSettings(configPath, write))
}

func readSettings(configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		eff, err := effectiveSettings(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		hash, err := fileHash(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		return c.JSON(SettingsResponse{
			Settings: settingsView(eff),
			Source:   sourceView(eff),
			ReadOnly: readOnlyKeys(),
			Hash:     hash,
		})
	}
}

func saveSettings(configPath string, write func(string, map[string]string, *[32]byte) (configfile.Outcome, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		var req SaveSettingsRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}
		if len(req.Values) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "values is empty; nothing to save"})
		}
		// The allowlist is checked here as well as in configfile, so a bad key is a
		// 400 naming it before anything reads the disk.
		keys := make([]string, 0, len(req.Values))
		for key := range req.Values {
			if ok, reason := config.EditableKey(key); !ok {
				return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: reason})
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)

		expected, err := parseHash(req.Hash)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: err.Error()})
		}
		outcome, err := write(configPath, req.Values, expected)
		if err != nil {
			// A decode failure means the value the client sent cannot be a config
			// value at all — a duration that does not parse, a bool that is neither.
			// That is the client's body, so it is a 422 with the loader's own words,
			// not the 500 writeFailure reserves for a disk or permission fault.
			var validation *config.ValidationError
			if errors.As(err, &validation) || isDecodeFailure(err) {
				return validationFailure(c, err)
			}
			return writeFailure(c, configPath, err)
		}
		eff, err := effectiveSettings(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		hash, err := fileHash(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		saved := outcome != configfile.OutcomeUnchanged
		return c.JSON(SaveSettingsResponse{
			Saved:           saved,
			Changed:         keys,
			RestartRequired: saved,
			Settings:        settingsView(eff),
			Source:          sourceView(eff),
			Hash:            hash,
		})
	}
}

// effectiveSettings reads the config the same way --print-config does. Unlike
// preflight's loadEffective it tolerates a box started without --config: the
// read still answers from defaults so the screen renders, and only the write
// needs a file.
func effectiveSettings(configPath string) (*config.Effective, error) {
	return config.Load(config.Options{FlagPath: configPath})
}

// settingsView is the writable surface as strings. It is deliberately narrow:
// the response carries only what PUT accepts, so the screen cannot offer a
// field the server would refuse.
func settingsView(eff *config.Effective) map[string]string {
	return map[string]string{
		"tick_interval":               eff.TickInterval.String(),
		"notify_interval":             eff.NotifyInterval.String(),
		"act":                         boolString(eff.Act),
		"rig_lifecycle.enabled":       boolString(eff.RigLifecycle.Enabled),
		"rig_lifecycle.idle_after":    eff.RigLifecycle.IdleAfter.String(),
		"rig_lifecycle.idle_poll_max": eff.RigLifecycle.IdlePollMax.String(),
	}
}

// sourceView reports where each writable value came from. The loader's own map
// uses the file's key names, so the same names work here without a translation
// table that could drift.
func sourceView(eff *config.Effective) map[string]string {
	out := make(map[string]string, len(eff.Sources)+3)
	for _, key := range []string{
		"tick_interval", "notify_interval", "act",
		"rig_lifecycle.enabled", "rig_lifecycle.idle_after", "rig_lifecycle.idle_poll_max",
	} {
		out[key] = eff.Sources[key]
		if out[key] == "" {
			out[key] = "default"
		}
	}
	return out
}

// readOnlyKeys is the refused surface with each key's reason. It is derived from
// config.EditableKey rather than written out here, so the screen and the refusal
// cannot disagree.
func readOnlyKeys() []ReadOnlyKey {
	out := make([]ReadOnlyKey, 0, 12)
	for _, key := range config.FileKeys() {
		if ok, _ := config.EditableKey(key); ok {
			continue
		}
		_, reason := config.EditableKey(key)
		out = append(out, ReadOnlyKey{Key: key, Reason: reason})
	}
	return out
}

// fileHash is the sha256 of the config file's bytes, hex encoded. A missing file
// hashes to the empty input's hash so a first save works from a read that found
// no file.
func fileHash(configPath string) (string, error) {
	if configPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", filepath.Base(configPath), err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

// parseHash turns the request's hex digest into the [32]byte the writer expects.
// An empty hash means "do not check", which is what a first save from a read of
// a missing file sends.
func parseHash(value string) (*[32]byte, error) {
	if value == "" {
		return nil, nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, errors.New("hash is not a sha256 hex digest")
	}
	var sum [32]byte
	copy(sum[:], decoded)
	return &sum, nil
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// isDecodeFailure reports whether the loader refused the document's shape rather
// than the write's mechanics. ValidateContent wraps a YAML decode error with
// "decode:" and the loader wraps a bad scalar with the key's own words; both are
// the client's body, not a server fault.
func isDecodeFailure(err error) bool {
	message := err.Error()
	return strings.Contains(message, "decode:") || strings.Contains(message, "invalid duration")
}
