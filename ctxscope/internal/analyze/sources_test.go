package analyze

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fsFixture lays out a home with two installed plugins (one disabled), a synced claude.ai skill,
// user and project commands, and hooks in a plugin and in the user settings.
func fsFixture(t *testing.T) FSResolver {
	t.Helper()
	home := t.TempDir()
	cwd := filepath.Join(home, "proj")
	on := filepath.Join(home, ".claude/plugins/cache/m/on/1.0")
	off := filepath.Join(home, ".claude/plugins/cache/m/off/1.0")
	write(t, filepath.Join(home, ".claude/plugins/installed_plugins.json"), `{"plugins":{
		"on@m":[{"installPath":"`+on+`"}],
		"off@m":[{"installPath":"`+off+`"}]}}`)
	write(t, filepath.Join(home, ".claude/settings.json"), `{
		"enabledPlugins":{"on@m":true,"off@m":true},
		"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"x"}]}]}}`)
	write(t, filepath.Join(cwd, ".claude/settings.local.json"), `{"enabledPlugins":{"off@m":false}}`)
	write(t, filepath.Join(on, "hooks/hooks.json"), `{"hooks":{"SessionStart":[],"UserPromptSubmit":[]}}`)
	write(t, filepath.Join(off, "hooks/hooks.json"), `{"hooks":{"SessionStart":[]}}`)
	write(t, filepath.Join(on, "skills/plain/SKILL.md"), "x")
	write(t, filepath.Join(home, ".claude/skills/synced/abc/pdf/SKILL.md"), "x")
	write(t, filepath.Join(home, ".claude/commands/mine.md"), "x")
	write(t, filepath.Join(cwd, ".claude/commands/list-skills.md"), "x")
	return NewFSResolver(home, cwd)
}

func TestFSResolverSkills(t *testing.T) {
	r := fsFixture(t)
	for name, want := range map[string]string{
		"list-skills":          "project ~/proj/.claude/commands/list-skills.md",
		"mine":                 "user ~/.claude/commands/mine.md",
		"plain":                "plugin on",
		"anthropic-skills:pdf": "claude.ai skill",
		"anthropic-skills:new": "plugin anthropic-skills",
		"on:thing":             "plugin on",
		"claude-api":           "built-in",
	} {
		if got, _ := r.SkillSource(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestFSResolverHookSources(t *testing.T) {
	r := fsFixture(t)
	// "off" is disabled by the project's settings.local.json, so its hook does not count.
	if got, want := r.HookSources("SessionStart"), []string{"plugin on", "settings ~/.claude/settings.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SessionStart: %v, want %v", got, want)
	}
	if got, want := r.HookSources("UserPromptSubmit"), []string{"plugin on"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UserPromptSubmit: %v, want %v", got, want)
	}
	if got := r.HookSources("Stop"); got != nil {
		t.Errorf("Stop: %v, want none", got)
	}
}
