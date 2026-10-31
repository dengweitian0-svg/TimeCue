"""通过真实 Go、SQLite 和模型边界验证验收脚本的快速完成竞争。"""
import importlib.util
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("chat_real_smoke", ROOT / "scripts/chat-real-smoke.py")
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class _ModelHandler(BaseHTTPRequestHandler):
    """仅在实际模型 HTTP 边界控制完整响应和在途响应。"""

    def log_message(self, *_arguments):
        """测试不输出无关 HTTP 访问日志。"""

    def do_POST(self):
        """返回真实 SSE，由 Go 保存事件和恢复原模型调用。"""
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        compact = "上下文检查点压缩" in request["messages"][0]["content"]
        last = request["messages"][-1]["content"]
        kind = "compact" if compact else "cancel" if "300条" in last else "normal" if "1到120" in last else "other"
        with self.server.lock:
            number = self.server.counts.get(kind, 0) + 1
            self.server.counts[kind] = number
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        def emit(content, finish=None):
            chunk = {"id": f"{kind}-{number}", "choices": [{"delta": {"content": content}}]}
            if finish:
                chunk["choices"][0]["finish_reason"] = finish
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())

        blocked_number = 2 if kind == self.server.fast_kind else 1
        blocked = kind == "cancel" or kind in {"normal", "compact"} and number == blocked_number
        if self.server.all_fast and kind == "normal":
            blocked = False
        try:
            if blocked:
                for index in range(5):
                    emit(f"真实在途片段{index}")
                self.wfile.flush()
                self.server.release.wait(30)
            else:
                emit("正常完成或恢复完成", "stop")
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            pass


class RecoveryScriptTests(unittest.TestCase):
    """通过原有公开验收入口检查竞争，不替代聊天业务服务。"""

    @classmethod
    def setUpClass(cls):
        """只构建一次正式 Go，编译产物和数据库留在已忽略的临时目录。"""
        parent = ROOT / ".gotmp-script-recovery"
        parent.mkdir(exist_ok=True)
        cls.directory = Path(tempfile.mkdtemp(dir=parent))
        cls.binary = cls.directory / "timecue-core.exe"
        subprocess.run(["go", "build", "-o", str(cls.binary), "./cmd/flutter"], cwd=ROOT, check=True)

    def run_recovery(self, fast_kind, all_fast=False):
        """让一次完整响应先于进度观察返回，后续尝试再保留真实在途窗口。"""
        server = ThreadingHTTPServer(("127.0.0.1", 0), _ModelHandler)
        server.counts, server.lock, server.release = {}, threading.Lock(), threading.Event()
        server.fast_kind = fast_kind
        server.all_fast = all_fast
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.addCleanup(server.release.set)
        key = self.directory / (fast_kind + ".env")
        key.write_text("TEST_MODEL=fixture-only\n", encoding="utf-8")
        with socket.socket() as port:
            port.bind(("127.0.0.1", 0))
            number = port.getsockname()[1]
        acceptance = smoke.Acceptance(SimpleNamespace(
            go=self.binary, env_file=key, provider="test", model="model",
            base_url=f"http://127.0.0.1:{server.server_port}", port=number,
            timeout=15, flutter_test=False))
        self.addCleanup(acceptance.stop)
        acceptance.configure()
        acceptance.start()
        acceptance.terminal(acceptance.create("请记住一段实际历史"))
        original_open = acceptance.opener.open
        delayed = False

        def delayed_open(request, *args, **kwargs):
            nonlocal delayed
            url = request.full_url if isinstance(request, urllib.request.Request) else request
            if "/events?" in url and "回顾此前" in acceptance.evidence["runs"][-1]["input"]:
                expected = sum("回顾此前" in item["input"] for item in acceptance.evidence["runs"])
                deadline = time.monotonic() + 10
                while server.counts.get("compact", 0) < expected:
                    if time.monotonic() > deadline:
                        raise AssertionError("摘要请求未到达实际模型边界")
                    time.sleep(0.01)
            if not delayed and "/events?" in url:
                latest = acceptance.evidence["runs"][-1]
                target = "1到120" in latest["input"] if fast_kind == "normal" else "回顾此前" in latest["input"]
                if target:
                    # 只延迟客户端的网络观察；终态与事件始终来自真实 Go/SQLite。
                    delayed = True
                    deadline = time.monotonic() + 10
                    while True:
                        with original_open(acceptance.base + "/v1/run/" + latest["run_id"]) as response:
                            if json.load(response)["finished_at"] is not None:
                                break
                        if time.monotonic() > deadline:
                            raise AssertionError("模型边界未按安排完成首次目标")
                        time.sleep(0.01)
            return original_open(request, *args, **kwargs)

        acceptance.opener.open = delayed_open
        if all_fast:
            with self.assertRaisesRegex(AssertionError, "恢复场景未覆盖.*三次均在中断前正常完成"):
                acceptance.cancel_and_resume()
            completed = [item for item in acceptance.evidence["checks"] if "completed_before_pause" in item]
            self.assertEqual(len(completed), 3)
            for item in acceptance.evidence["runs"]:
                if "1到120" in item["input"]:
                    self.assertEqual(item["run"]["status"], "succeeded")
            return
        acceptance.cancel_and_resume()
        self.assertGreaterEqual(server.counts[fast_kind], 3)
        checks = acceptance.evidence["checks"]
        self.assertTrue(any("model_recovery" in item for item in checks))
        self.assertTrue(any("summary_recovery" in item for item in checks))

    def test_completed_model_before_pause_is_retried(self):
        """普通模型正常完成竞争不得被误报为退出恢复失败。"""
        self.run_recovery("normal")

    def test_completed_summary_before_pause_is_retried(self):
        """摘要已经切换并正常完成时重新准备场景，不读取不存在的 compact 在途行。"""
        self.run_recovery("compact")

    def test_always_completed_model_reports_uncovered_scenario(self):
        """三次窗口均错过时保留真实成功，只报告场景未覆盖。"""
        self.run_recovery("normal", all_fast=True)


if __name__ == "__main__":
    unittest.main()
