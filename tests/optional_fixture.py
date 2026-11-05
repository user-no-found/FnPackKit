"""Isolated, local-only Git fixtures shared by optional package contract tests."""
import json
from pathlib import Path
import subprocess
import tempfile

from buildflow.contracts import Context, Project
from buildflow.runner import Runner
from fnpackkit.optional_packages import selection_prepare


class OptionalFixture:
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="fnpackkit-optional-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.main = self.root / "main"
        self.main.mkdir()
        self.git(self.main, "init", "-q")
        self.git(self.main, "remote", "add", "origin", "git@github.com:owner/FnDSH.git")
        self.work = self.root / "work"
        self.work.mkdir()
        self.data = {"app_id": "dsh", "display_name": "DSH", "version": "1.2.3",
                     "buildflow": {"schema": 1, "targets": ["fpk"], "steps": []},
                     "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "payload.json",
                                   "optional_packages": {"schema": 1}}}
        self.project = Project(self.main / "project.json", self.main, self.data, self.data["buildflow"])
        self.events = []
        self.ctx = Context(self.project, self.work, self.root / "output",
                           Runner(self.work / "commands.log", self.events.append))

    @staticmethod
    def git(path, *args):
        result = subprocess.run(["/usr/bin/git", "-c", "user.name=Fixture", "-c",
                                 "user.email=fixture@example.invalid", *args], cwd=path,
                                check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        return result.stdout.strip()

    def package(self, feature="cyberstrike", *, committed=False, **fields):
        name = "FnDSHOptional-" + feature
        path = self.main / "optional" / name
        path.mkdir(parents=True)
        self.git(path, "init", "-q")
        self.git(path, "remote", "add", "origin", f"git@github.com:owner/{name}.git")
        manifest = {"schema": 1, "id": feature, "label": feature.title(), "project": "FnDSH",
                    "patches": [], "files": [], **fields}
        (path / "fpk-option.json").write_text(json.dumps(manifest))
        if committed:
            self.git(path, "add", ".")
            self.git(path, "commit", "-qm", "fixture")
        return path

    def manifest(self, path, **fields):
        data = json.loads((path / "fpk-option.json").read_text())
        data.update(fields)
        (path / "fpk-option.json").write_text(json.dumps(data))

    def choose(self, features=None, *, no_features=False):
        prepare = selection_prepare(self.project, features=features, no_features=no_features)
        prepare(self.ctx)
        return self.ctx.metadata["optional_packages"]

    def target(self):
        path = self.work / "source"
        path.mkdir()
        self.git(path, "init", "-q")
        self.git(path, "remote", "add", "origin", "https://example.invalid/upstream.git")
        (path / "existing.txt").write_text("original\n")
        self.git(path, "add", ".")
        self.git(path, "commit", "-qm", "fixture")
        return path

    @staticmethod
    def text_patch(path, old="original", new="enhanced"):
        (path / "change.patch").write_text(
            "diff --git a/existing.txt b/existing.txt\n"
            "--- a/existing.txt\n+++ b/existing.txt\n@@ -1 +1 @@\n"
            f"-{old}\n+{new}\n")
