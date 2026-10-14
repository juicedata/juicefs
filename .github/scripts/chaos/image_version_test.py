#!/usr/bin/env python3

import subprocess
import tempfile
import unittest
from pathlib import Path


class ImageVersionTest(unittest.TestCase):
    def test_workflow_image_versions(self):
        workflow = Path(__file__).resolve().parents[2] / "workflows/chaos.yml"
        commands = [
            line.strip() for line in workflow.read_text().splitlines()
            if line.strip().startswith("version=")
        ]
        self.assertEqual(len(commands), 2)
        versions = {
            "1.4.1+2026-10-14.e7c0244": "1.4.1",
            "1.5.0-dev+2026-10-14.e7c0244": "1.5.0",
            "1.4.2-rc1+2026-10-14.e7c0244": "1.4.2",
            "1.4.2": "1.4.2",
        }
        with tempfile.TemporaryDirectory() as directory:
            juicefs = Path(directory) / "juicefs"
            for version, expected in versions.items():
                juicefs.write_text(f"#!/bin/sh\necho 'juicefs version {version}'\n")
                juicefs.chmod(0o755)
                for command in commands:
                    with self.subTest(version=version, command=command):
                        result = subprocess.run(
                            ["bash", "-eu", "-c", command + '\nprintf "%s" "$version"'],
                            cwd=directory, capture_output=True, text=True, check=True,
                        )
                        self.assertEqual(result.stdout, expected)


if __name__ == "__main__":
    unittest.main()
