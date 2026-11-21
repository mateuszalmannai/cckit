"""memory: frontmatter round-trip, index rendering, check, and the new/rm/index commands on a temp tree."""
import contextlib
import io
import tempfile
import json
import unittest
from pathlib import Path
from unittest import mock

import importlib.machinery
import importlib.util

_path = str(Path(__file__).resolve().parent.parent / "bin" / "memory")
_loader = importlib.machinery.SourceFileLoader("memory_tree", _path)
mt = importlib.util.module_from_spec(importlib.util.spec_from_loader("memory_tree", _loader))
_loader.exec_module(mt)


def memory(name, mtype, description="d", title=None, hook=None, body="Fact.", extra_top=""):
    meta = f"  type: {mtype}\n" + (f"  title: {title}\n" if title else "") + (f"  hook: {hook}\n" if hook else "")
    return f"---\nname: {name}\n{extra_top}description: {description}\nmetadata:\n{meta}---\n\n{body}\n"


class Tree(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)

    def put(self, rel, text):
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def run_cli(self, *args):
        out, errs = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(errs):
            rc = mt.main(["--root", str(self.root), *args])
        return rc, out.getvalue(), errs.getvalue()


class Frontmatter(Tree):
    def test_quoted_values_and_metadata_are_read(self):
        fm, _ = mt.split_frontmatter('---\nname: a\ndescription: "say \\"hi\\": now"\nmetadata: \n'
                                     "  type: feedback\n  title: 'It''s'\n---\nbody")
        top, meta = mt.parse_frontmatter(fm)
        self.assertEqual(top["description"], 'say "hi": now')
        self.assertEqual(meta, {"type": "feedback", "title": "It's"})

    def test_set_metadata_adds_then_replaces_and_keeps_the_rest(self):
        path = self.put("feedback/a.md", memory("a", "feedback", body="Line one.\n\n[[b]]"))
        mt.set_metadata(path, "title", "VM paste: flat")
        mt.set_metadata(path, "hook", "short")
        mt.set_metadata(path, "title", "Second")
        text = path.read_text()
        self.assertIn("  title: Second\n", text)
        self.assertEqual(text.count("title:"), 1)
        self.assertTrue(text.endswith("---\n\nLine one.\n\n[[b]]\n"))
        top, meta = mt.parse_frontmatter(mt.split_frontmatter(text)[0])
        self.assertEqual((meta["title"], meta["hook"], meta["type"]), ("Second", "short", "feedback"))

    def test_value_with_colon_is_quoted(self):
        path = self.put("feedback/a.md", memory("a", "feedback"))
        mt.set_metadata(path, "title", "VM paste: flat")
        self.assertIn('  title: "VM paste: flat"', path.read_text())


class Index(Tree):
    def test_listed_types_get_a_line_each_others_one_line_per_leaf(self):
        self.put("MEMORY.md", "# Memory index\n\nKeep this header.\n\n## feedback\nold\n")
        self.put("feedback/repo-b/z.md", memory("z", "feedback", "desc z", title="Zed", hook="hook z"))
        self.put("feedback/repo-a/y.md", memory("y", "feedback", "desc y"))
        self.put("feedback/top.md", memory("top", "feedback", "desc top", title="Top"))
        self.put("project/p/x.md", memory("x", "project", title="Xray"))
        self.put("project/p/w.md", memory("w", "project", title="Whiskey"))
        self.put("project/p/deep/v.md", memory("v", "project", title="Victor"))
        new, _ = mt.build_index(self.root, mt.load(self.root))
        self.assertEqual(new, (
            "# Memory index\n\nKeep this header.\n\n"
            "## feedback\n"
            "- [Top](feedback/top.md) — desc top\n\n"
            "### repo-a — `feedback/repo-a/`\n- [Y](feedback/repo-a/y.md) — desc y\n\n"
            "### repo-b — `feedback/repo-b/`\n- [Zed](feedback/repo-b/z.md) — hook z\n\n"
            "## project\n"
            "### p — `project/p/` (2)\nWhiskey · Xray\n\n"
            "### p/deep — `project/p/deep/` (1)\nVictor\n"))

    def test_index_command_writes_once_then_reports_unchanged(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.assertEqual(self.run_cli("index", "--dry-run")[0], 1)
        self.run_cli("index")
        self.assertTrue((self.root / "MEMORY.md").read_text().startswith("# Memory index\n\n## user\n"))
        rc, out, _ = self.run_cli("index", "--dry-run")
        self.assertEqual((rc, out.strip()), (0, "MEMORY.md is up to date"))


class FlatLayout(Tree):
    """Claude Code's built-in layout: files directly in the root, a flat MEMORY.md."""

    def test_rebuild_keeps_hand_written_lines_and_adds_sections(self):
        self.put("MEMORY.md", "- [Use pnpm](feedback_pnpm.md) — not npm, lockfile is pnpm\n"
                              "- [Role](user_role.md) — data scientist\n")
        self.put("feedback_pnpm.md", memory("feedback_pnpm", "feedback", "Use pnpm, not npm"))
        self.put("user_role.md", memory("user_role", "user", "The user is a data scientist"))
        self.put("project_x.md", memory("project_x", "project", "Project X"))
        new, _ = mt.build_index(self.root, mt.load(self.root))
        self.assertEqual(new, (
            "# Memory index\n\n"
            "## user\n- [Role](user_role.md) — data scientist\n\n"
            "## feedback\n- [Use pnpm](feedback_pnpm.md) — not npm, lockfile is pnpm\n\n"
            "## project\n- [Project x](project_x.md) — Project X\n"))

    def test_flat_files_are_valid_and_a_bad_type_is_kept_and_reported(self):
        self.put("feedback_a.md", memory("feedback_a", "feedback", title="A"))
        self.put("odd.md", memory("odd", "note", title="Odd"))
        errors, _ = mt.problems(self.root, mt.load(self.root))
        self.assertEqual([e for e in errors if "out of date" not in e],
                         ["odd.md: type `note` is not one of user, feedback, project, reference"])
        new, _ = mt.build_index(self.root, mt.load(self.root))
        self.assertIn("## other\n- [Odd](odd.md)", new)

    def test_new_flat_needs_a_type(self):
        with self.assertRaises(SystemExit):
            self.run_cli("new", "loose", "--no-edit")
        self.run_cli("new", "feedback_tabs", "--type", "feedback", "--no-edit")
        self.assertIn("type: feedback", (self.root / "feedback_tabs.md").read_text())
        self.assertIn("[Feedback tabs](feedback_tabs.md)", (self.root / "MEMORY.md").read_text())


class Check(Tree):
    def test_reports_errors_and_ignores_links_in_code(self):
        self.put("feedback/a.md", memory("dup", "feedback", title="A", body="see [[nobody]] and `[[:space:]]`"))
        self.put("project/b.md", memory("dup", "feedback", title="B"))
        self.put("project/c.md", "no frontmatter\n")
        self.put("notes/d.md", memory("d", "project", title="D"))
        self.put(".git/ignored.md", "x")
        errors, warnings = mt.problems(self.root, mt.load(self.root))
        joined = "\n".join(errors)
        self.assertIn("name `dup` is used by 2 files", joined)
        self.assertIn("project/b.md: type `feedback` but stored under project/", joined)
        self.assertIn("project/c.md: no frontmatter", joined)
        self.assertIn("notes/d.md: not under", joined)
        self.assertIn("MEMORY.md is out of date", joined)
        self.assertNotIn("ignored", joined)
        self.assertTrue(any("[[nobody]]" in w for w in warnings))
        self.assertFalse(any(":space:" in w for w in warnings))

    def test_clean_tree_passes(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.run_cli("index")
        self.assertEqual(self.run_cli("check")[0], 0)


class StatusAndHooks(Tree):
    def setUp(self):
        super().setUp()
        cache = tempfile.TemporaryDirectory()
        self.addCleanup(cache.cleanup)
        patcher = mock.patch.dict(mt.os.environ, {"XDG_CACHE_HOME": cache.name})
        patcher.start()
        self.addCleanup(patcher.stop)

    def hook(self, event, payload=None):
        with mock.patch.object(mt.sys, "stdin", io.StringIO(json.dumps(payload or {}))):
            rc, out, _ = self.run_cli("hook", event)
        self.assertEqual(rc, 0)
        return json.loads(out)["hookSpecificOutput"]["additionalContext"] if out.strip() else ""

    def test_status_is_green_then_red_when_stale_and_follows_changes(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.run_cli("index")
        self.assertEqual(self.run_cli("status")[1].strip(), "🧠 1 · 4/200 ✓")
        self.put("user/you.md", memory("you", "user", title="You"))   # cache must not hide this
        self.assertEqual(self.run_cli("status")[1].strip(), "🧠 2 · index stale ✗")
        self.assertIn("\033[31m", self.run_cli("status", "--color")[1])

    def test_status_warns_near_the_size_limits(self):
        for i in range(160):
            self.put(f"user/m{i:03}.md", memory(f"m{i:03}", "user", title=f"M{i}"))
        self.run_cli("index")
        self.assertEqual(self.run_cli("status")[1].strip(), "🧠 160 · 163/200 ⚠")
        _, warnings = mt.problems(self.root, mt.load(self.root))
        self.assertTrue(any("163 lines" in w for w in warnings))

    def test_byte_limit_is_an_error_even_with_few_lines(self):
        for i in range(40):
            self.put(f"user/m{i}.md", memory(f"m{i}", "user", title=f"M{i}", hook="x" * 700))
        self.run_cli("index")
        errors, _ = mt.problems(self.root, mt.load(self.root))
        self.assertTrue(any("KB; past 25 KB" in e for e in errors))
        self.assertIn("/25 KB", self.run_cli("status")[1])

    def test_post_tool_use_rebuilds_the_index_and_ignores_other_files(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.assertEqual(self.hook("post-tool-use", {"tool_input": {"file_path": "/elsewhere/x.md"}}), "")
        self.assertFalse((self.root / "MEMORY.md").exists())
        note = self.hook("post-tool-use", {"tool_input": {"file_path": str(self.root / "user/me.md")}})
        self.assertIn("rebuilt MEMORY.md", note)
        self.assertIn("[Me](user/me.md)", (self.root / "MEMORY.md").read_text())

    def test_post_tool_use_undoes_a_hand_edit_and_reports_errors(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.run_cli("index")
        (self.root / "MEMORY.md").write_text("# Memory index\n\n## user\n- [Hand](user/me.md) — mine\n")
        self.put("user/bad.md", "no frontmatter\n")
        note = self.hook("post-tool-use", {"tool_input": {"file_path": str(self.root / "MEMORY.md")}})
        self.assertIn("replaced the hand edit", note)
        self.assertIn("user/bad.md: no frontmatter", note)
        self.assertIn("[Me](user/me.md)", (self.root / "MEMORY.md").read_text())

    def test_session_start_reports_only_problems(self):
        self.put("user/me.md", memory("me", "user", title="Me"))
        self.assertIn("out of date", self.hook("session-start"))
        self.run_cli("index")
        self.assertEqual(self.hook("session-start"), "")

    def test_missing_root_is_silent(self):
        rc, out, _ = mt.main(["--root", str(self.root / "nope"), "status"]), "", ""
        self.assertEqual(rc, 0)


class MoveAndMigrate(Tree):
    def flat_tree(self):
        self.put("MEMORY.md", "- [Use pnpm](feedback_use_pnpm.md) — not npm\n"
                              "- [Fritz panel](project_fritz_panel.md) — dashboard state\n")
        self.put("feedback_use_pnpm.md", memory("feedback_use_pnpm", "feedback", "Use pnpm, not npm"))
        self.put("project_fritz_panel.md", memory("project_fritz_panel", "project", "Fritz dashboard",
                                                 body="Follows [[feedback_use_pnpm]]; `[[feedback_use_pnpm]]` in code too."))
        self.put("user_role.md", memory("user_role", "user", "Role"))

    def test_plan_proposes_type_folder_and_kebab_name_with_blank_folder(self):
        self.flat_tree()
        rc, out, _ = self.run_cli("migrate", "--plan")
        rows = [l.split("\t") for l in out.splitlines() if l and not l.startswith("#")]
        self.assertEqual(rows, [["feedback_use_pnpm.md", "feedback", "", "use-pnpm"],
                                ["project_fritz_panel.md", "project", "", "fritz-panel"],
                                ["user_role.md", "user", "", "role"]])

    def test_apply_moves_renames_relinks_keeps_titles_and_reindexes(self):
        self.flat_tree()
        plan = self.put("plan.tsv", "# comment\n"
                        "feedback_use_pnpm.md\tfeedback\tweb-shop\tuse-pnpm\n"
                        "project_fritz_panel.md\tproject\tfritz-monitoring\tfritz-panel\n")
        rc, out, _ = self.run_cli("migrate", "--apply", str(plan), "--dry-run")
        self.assertEqual(rc, 0)
        self.assertIn("would move feedback_use_pnpm.md → feedback/web-shop/use-pnpm.md", out)
        self.assertTrue((self.root / "feedback_use_pnpm.md").exists())          # dry run wrote nothing

        rc, _, _ = self.run_cli("migrate", "--apply", str(plan))
        self.assertEqual(rc, 0)
        moved = (self.root / "feedback/web-shop/use-pnpm.md").read_text()
        self.assertIn("name: use-pnpm\n", moved)
        self.assertIn("  title: Use pnpm\n", moved)                            # from the old index line
        self.assertIn("  hook: not npm\n", moved)
        panel = (self.root / "project/fritz-monitoring/fritz-panel.md").read_text()
        self.assertIn("Follows [[use-pnpm]]", panel)
        index = (self.root / "MEMORY.md").read_text()
        self.assertIn("- [Use pnpm](feedback/web-shop/use-pnpm.md) — not npm", index)
        self.assertIn("### fritz-monitoring — `project/fritz-monitoring/` (1)\nFritz panel", index)
        self.assertTrue((self.root / "user_role.md").exists())                  # not in the plan: stays flat
        self.assertEqual(self.run_cli("check")[0], 0)

    def test_apply_refuses_the_whole_plan_on_any_problem(self):
        self.flat_tree()
        self.put("feedback/taken.md", memory("taken", "feedback", title="Taken"))
        plan = self.put("plan.tsv",
                        "feedback_use_pnpm.md\tfeedback\t\ttaken\n"            # file exists + name clash
                        "project_fritz_panel.md\tproject\t../escape\tok\n"     # unsafe folder
                        "user_role.md\tnotes\t\tBad_Name\n"                    # bad type and name
                        "missing.md\tuser\t\tx\n")
        rc, _, errs = self.run_cli("migrate", "--apply", str(plan))
        self.assertEqual(rc, 1)
        for text in ("feedback/taken.md exists", "name `taken` would be used by 2 files", "folder `../escape`",
                     "type `notes`", "name `Bad_Name`", "missing.md): no such memory", "nothing moved"):
            self.assertIn(text, errs)
        self.assertTrue((self.root / "feedback_use_pnpm.md").exists())

    def test_apply_refuses_two_rows_with_one_target(self):
        self.flat_tree()
        plan = self.put("plan.tsv", "feedback_use_pnpm.md\tfeedback\t\tsame\nuser_role.md\tfeedback\t\tsame\n")
        rc, _, errs = self.run_cli("migrate", "--apply", str(plan))
        self.assertEqual(rc, 1)
        self.assertIn("also the target of row 1", errs)

    def test_mv_moves_renames_and_needs_retype_across_types(self):
        self.put("feedback/a/old-name.md", memory("old-name", "feedback", title="Old"))
        self.put("user/me.md", memory("me", "user", title="Me", body="see [[old-name|that rule]]"))
        self.assertEqual(self.run_cli("mv", "old-name", "feedback/b", "--name", "new-name")[0], 0)
        self.assertTrue((self.root / "feedback/b/new-name.md").exists())
        self.assertFalse((self.root / "feedback/a").exists())                   # emptied folder removed
        self.assertIn("[[new-name|that rule]]", (self.root / "user/me.md").read_text())
        rc, _, errs = self.run_cli("mv", "new-name", "project/x")
        self.assertEqual(rc, 1)
        self.assertIn("--retype", errs)
        self.assertEqual(self.run_cli("mv", "new-name", "project/x", "--retype")[0], 0)
        self.assertIn("  type: project", (self.root / "project/x/new-name.md").read_text())

    def test_bad_plan_line_is_rejected(self):
        plan = self.put("plan.tsv", "only two\tcells\n")
        with self.assertRaises(SystemExit) as ctx:
            self.run_cli("migrate", "--apply", str(plan))
        self.assertIn("expected 4 tab-separated cells", str(ctx.exception))


class Commands(Tree):
    def test_new_then_rm(self):
        self.put("feedback/other.md", memory("other", "feedback", title="Other", body="see [[my-rule]]"))
        self.run_cli("new", "feedback/repo/my-rule", "--no-edit")
        path = self.root / "feedback/repo/my-rule.md"
        top, meta = mt.parse_frontmatter(mt.split_frontmatter(path.read_text())[0])
        self.assertEqual((top["name"], meta["type"], meta["title"]), ("my-rule", "feedback", "My rule"))
        self.assertIn("[My rule](feedback/repo/my-rule.md)", (self.root / "MEMORY.md").read_text())

        _, _, errs = self.run_cli("rm", "my-rule", "--yes")
        self.assertIn("linked from: other", errs)
        self.assertFalse((self.root / "feedback/repo").exists())
        self.assertNotIn("my-rule", (self.root / "MEMORY.md").read_text())

    def test_new_refuses_a_taken_name_and_an_unknown_type(self):
        self.put("user/me.md", memory("me", "user"))
        with self.assertRaises(SystemExit):
            self.run_cli("new", "feedback/x/me", "--no-edit")
        with self.assertRaises(SystemExit):
            self.run_cli("new", "notes/x", "--no-edit")

    def test_resolve_by_name_part_and_path(self):
        self.put("user/alpha-one.md", memory("alpha-one", "user"))
        self.put("user/alpha-two.md", memory("alpha-two", "user"))
        memories = mt.load(self.root)
        self.assertEqual(mt.resolve(self.root, memories, "two").name, "alpha-two")
        self.assertEqual(mt.resolve(self.root, memories, "user/alpha-one").name, "alpha-one")
        with self.assertRaises(SystemExit) as ctx:
            mt.resolve(self.root, memories, "alpha")
        self.assertIn("matches 2", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
