"""验证发行验收 CLI 在 Windows 非中文输出编码下可用。"""
import os
from pathlib import Path
import subprocess
import sys
import unittest


class ReleaseSmokeConsoleTests(unittest.TestCase):
    """通过公开 CLI 捕获实际编码故障。"""

    def test_help_supports_cp1252_parent(self):
        """英文 Windows 的输出管道不能使中文帮助和验收输出报错。"""
        for script, description in [("release-smoke.py", "验证完整发行目录"), ("chat-real-smoke.py", "通过正式 Go 入口")]:
            with self.subTest(script=script):
                result = subprocess.run(
                    [sys.executable, str(Path(__file__).with_name(script)), "--help"],
                    env={**os.environ, "PYTHONIOENCODING": "cp1252"}, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr.decode("ascii", errors="replace"))
                self.assertIn(description, result.stdout.decode("utf-8"))


if __name__ == "__main__":
    unittest.main()
