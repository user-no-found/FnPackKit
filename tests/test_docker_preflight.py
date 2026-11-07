"""Shared pre-deployment probe errors and distributable payload contract."""
import shutil
import unittest

from fnpackkit.docker import preflight_file
from fnpackkit.docker_preflight import check
from fnpackkit.execution_directory import execution_directory


class DockerPreflightTests(unittest.TestCase):
    def test_engine_and_runtime(self):
        directory = execution_directory()
        self.addCleanup(shutil.rmtree, directory)
        binary = directory / "docker"
        binary.write_text('#!/bin/sh\ncase "$1" in info) printf \'{"runc":{}}\\n\';; *) exit 0;; esac\n')
        binary.chmod(0o700)
        check(str(binary))
        with self.assertRaisesRegex(RuntimeError, "nvidia"):
            check(str(binary), "nvidia")
        binary.write_text("#!/bin/sh\nexit 1\n")
        with self.assertRaisesRegex(RuntimeError, "Compose"):
            check(str(binary))

    def test_payload_and_input_boundaries(self):
        declaration = preflight_file()
        self.assertTrue(declaration.source.is_file())
        self.assertEqual(declaration.destination, "cmd/hooks/fnpack_docker.py")
        self.assertEqual(declaration.mode, 0o755)
        with self.assertRaises(ValueError):
            check("docker")
        with self.assertRaises(ValueError):
            check("/usr/bin/docker", "--privileged")


if __name__ == "__main__":
    unittest.main()
