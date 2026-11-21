package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver attributes named items to where they live on disk.
type Resolver interface {
	SkillSource(name string) (source, hint string)
	AgentSource(name string) (source, hint string)
	MemorySource(path string) (source, hint string)
	// HookSources lists every configured source of hooks for event: enabled plugins whose
	// hooks/hooks.json registers it and settings files with a hooks entry for it.
	HookSources(event string) []string
	Short(path string) string // path with $HOME shown as ~
}

// Plugin is an installed, enabled plugin.
type Plugin struct {
	Name string // "superpowers"
	Path string // its install directory
}

// FSResolver looks items up in ~/.claude, in the project's .claude directories (cwd and its
// parents up to $HOME) and in the enabled plugins. Build it with NewFSResolver so Plugins is
// filled in.
type FSResolver struct {
	Home    string
	Cwd     string
	Plugins []Plugin
}

// NewFSResolver reads the installed plugins and keeps the enabled ones.
func NewFSResolver(home, cwd string) FSResolver {
	r := FSResolver{Home: home, Cwd: cwd}
	r.Plugins = r.loadPlugins()
	return r
}

func (r FSResolver) Short(p string) string {
	if r.Home != "" && strings.HasPrefix(p, r.Home+"/") {
		return "~" + p[len(r.Home):]
	}
	return p
}

func (r FSResolver) projectDirs() []string {
	var dirs []string
	for d := r.Cwd; d != "" && d != "/"; d = filepath.Dir(d) {
		if d == r.Home {
			break
		}
		dirs = append(dirs, filepath.Join(d, ".claude"))
	}
	return dirs
}

// settingsFiles lists the settings files Claude Code merges, lowest precedence first.
func (r FSResolver) settingsFiles() []string {
	files := []string{filepath.Join(r.Home, ".claude", "settings.json")}
	dirs := r.projectDirs()
	for i := len(dirs) - 1; i >= 0; i-- {
		files = append(files, filepath.Join(dirs[i], "settings.json"), filepath.Join(dirs[i], "settings.local.json"))
	}
	return files
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// loadPlugins returns the installed plugins that the merged enabledPlugins settings turn on.
// With no enabledPlugins anywhere, every installed plugin counts as enabled.
func (r FSResolver) loadPlugins() []Plugin {
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if !readJSON(filepath.Join(r.Home, ".claude", "plugins", "installed_plugins.json"), &installed) {
		return nil
	}
	enabled := map[string]bool{}
	for _, f := range r.settingsFiles() {
		var s struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if readJSON(f, &s) {
			for k, v := range s.EnabledPlugins {
				enabled[k] = v
			}
		}
	}
	var out []Plugin
	for key, versions := range installed.Plugins {
		if len(versions) == 0 || (len(enabled) > 0 && !enabled[key]) {
			continue
		}
		name, _, _ := strings.Cut(key, "@")
		out = append(out, Plugin{Name: name, Path: versions[0].InstallPath})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func pluginHint(plugin string) string {
	return "plugin skill: disable the whole plugin with /plugin or enabledPlugins \"" + plugin + "@…\": false"
}

func (r FSResolver) SkillSource(name string) (string, string) {
	if prefix, base, ok := strings.Cut(name, ":"); ok {
		if prefix == "anthropic-skills" {
			if m, _ := filepath.Glob(filepath.Join(r.Home, ".claude", "skills", "synced", "*", base)); len(m) > 0 {
				return "claude.ai skill", "synced from claude.ai: turn it off in claude.ai's skill settings"
			}
		}
		return "plugin " + prefix, pluginHint(prefix)
	}
	for _, d := range r.projectDirs() {
		if p := filepath.Join(d, "skills", name); exists(p) {
			return "project " + r.Short(p), "project skill: move it out of " + r.Short(filepath.Dir(p)) + " or shorten its description"
		}
		if p := filepath.Join(d, "commands", name+".md"); exists(p) {
			return "project " + r.Short(p), "project command: move it out of " + r.Short(filepath.Dir(p)) + " or shorten its description"
		}
	}
	if p := filepath.Join(r.Home, ".claude", "skills", name); exists(p) {
		return "user " + r.Short(p), "user skill: move it out of ~/.claude/skills or shorten the description in its SKILL.md"
	}
	if p := filepath.Join(r.Home, ".claude", "commands", name+".md"); exists(p) {
		return "user " + r.Short(p), "user command: move it out of ~/.claude/commands or shorten its description"
	}
	for _, pl := range r.Plugins {
		if exists(filepath.Join(pl.Path, "skills", name)) || exists(filepath.Join(pl.Path, "commands", name+".md")) {
			return "plugin " + pl.Name, pluginHint(pl.Name)
		}
	}
	return "built-in", "bundled with Claude Code; check /skills"
}

func (r FSResolver) AgentSource(name string) (string, string) {
	if plugin, _, ok := strings.Cut(name, ":"); ok {
		return "plugin " + plugin, pluginHint(plugin)
	}
	for _, d := range r.projectDirs() {
		if p := filepath.Join(d, "agents", name+".md"); exists(p) {
			return "project " + r.Short(p), "project agent: delete it or move the <example> blocks out of its description"
		}
	}
	if p := filepath.Join(r.Home, ".claude", "agents", name+".md"); exists(p) {
		return "user " + r.Short(p), "user agent: delete it or move the <example> blocks out of its description"
	}
	return "built-in", "bundled with Claude Code"
}

func (r FSResolver) MemorySource(path string) (string, string) {
	short := r.Short(path)
	base := filepath.Base(path)
	switch base {
	case "MEMORY.md":
		return short, "auto-memory index: keep it to one line per memory, prune stale ones"
	case "CLAUDE.md", "CLAUDE.local.md":
		return short, "trim it; move rarely needed detail into a skill or a doc the agent reads on demand"
	default:
		return short, "pulled in by an @import in a CLAUDE.md; remove the import or trim the file"
	}
}

type hooksFile struct {
	Hooks map[string]json.RawMessage `json:"hooks"`
}

func (r FSResolver) HookSources(event string) []string {
	var out []string
	for _, pl := range r.Plugins {
		var h hooksFile
		if readJSON(filepath.Join(pl.Path, "hooks", "hooks.json"), &h) && h.Hooks[event] != nil {
			out = append(out, "plugin "+pl.Name)
		}
	}
	for _, f := range r.settingsFiles() {
		var h hooksFile
		if readJSON(f, &h) && h.Hooks[event] != nil {
			out = append(out, "settings "+r.Short(f))
		}
	}
	return out
}
