import importlib.util
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("release", Path(__file__).resolve().parents[1] / "scripts/release.py")
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.env = self.root / ".env"
        (self.root / release.PROJECT).parent.mkdir()
        # Keep the fixture independent of the repository's current release version.
        (self.root / release.PROJECT).write_text(
            "{\n"
            "\tDebug = {\n"
            "\t\tMARKETING_VERSION = 2.7.1;\n"
            "\t\tCURRENT_PROJECT_VERSION = 62;\n"
            "\t};\n"
            "\tRelease = {\n"
            "\t\tMARKETING_VERSION = 2.7.1;\n"
            "\t\tCURRENT_PROJECT_VERSION = 62;\n"
            "\t};\n"
            "}\n"
        )
        (self.root / ".env.example").write_text("version=v2.7.1\n")
        self.env.write_text("# previous version=v2.7.1\nOTHER=keep\nversion=v2.7.1\n")
        (self.root / ".gitignore").write_text(".env\n")
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")
        self.git("config", "core.hooksPath", ".git/hooks")
        self.git("add", ".")
        self.git("commit", "-m", "initial")

    def git(self, *args):
        return release.git(self.root, *args)

    def test_patch_release_commit_and_annotated_tag(self):
        release.release(self.root, self.env)
        self.assertEqual(release.check(self.root, self.env)[0:2], ("v2.7.2", "63"))
        self.assertIn("OTHER=keep\n", self.env.read_text())
        self.assertIn("# previous version=v2.7.1\n", self.env.read_text())
        self.assertEqual(self.git("cat-file", "-t", "v2.7.2"), "tag")
        self.assertEqual(self.git("rev-parse", "v2.7.2^{}"), self.git("rev-parse", "HEAD"))
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertEqual(set(self.git("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").splitlines()), {".env.example", str(release.PROJECT)})

    def test_explicit_version(self):
        release.release(self.root, self.env, "v3.0.0")
        self.assertEqual(release.check(self.root, self.env, "v3.0.0")[0], "v3.0.0")

    def test_invalid_env(self):
        for value in ("", "version=v2.7.1\nversion=v2.7.1\n", " version=v2.7.1\n", "version= v2.7.1\n", "version=v02.7.1\n", "version=v2.7.1 \n"):
            with self.subTest(value=value):
                self.env.write_text(value)
                with self.assertRaises(ValueError):
                    release.read_env(self.env)

    def test_rejections_do_not_mutate_version_or_head(self):
        for mode in ("untracked", "unstaged", "staged", "tag", "mismatch", "invalid", "downgrade"):
            with self.subTest(mode=mode):
                head = self.git("rev-parse", "HEAD")
                original = self.env.read_text()
                dirty = self.root / "untracked"
                if mode == "untracked":
                    dirty.write_text("dirty")
                elif mode in ("unstaged", "staged"):
                    (self.root / ".gitignore").write_text(".env\nnew-entry\n")
                    if mode == "staged":
                        self.git("add", ".gitignore")
                elif mode == "tag":
                    self.git("tag", "v2.7.2")
                elif mode == "mismatch":
                    self.env.write_text("version=v2.7.0\n")
                before = self.env.read_text()
                with self.assertRaises(ValueError):
                    release.release(self.root, self.env, {"invalid": "v1.2", "downgrade": "v2.7.0"}.get(mode, ""))
                self.assertEqual(self.env.read_text(), before)
                self.assertEqual(self.git("rev-parse", "HEAD"), head)
                self.env.write_text(original)
                dirty.unlink(missing_ok=True)
                self.git("restore", "--staged", "--worktree", ".gitignore")
                if mode == "tag":
                    self.git("tag", "-d", "v2.7.2")

    def test_tag_version_mismatch(self):
        with self.assertRaises(ValueError):
            release.check(self.root, self.env, "v9.0.0")

    def test_failed_commit_does_not_create_tag(self):
        hook = self.root / ".git/hooks/pre-commit"
        hook.write_text("#!/bin/sh\nexit 1\n")
        hook.chmod(0o755)
        head = self.git("rev-parse", "HEAD")
        with self.assertRaises(release.subprocess.CalledProcessError):
            release.release(self.root, self.env)
        self.assertEqual(self.git("rev-parse", "HEAD"), head)
        self.assertEqual(self.git("tag", "--list"), "")
        self.assertEqual(release.check(self.root, self.env)[0], "v2.7.2")


if __name__ == "__main__":
    unittest.main()
