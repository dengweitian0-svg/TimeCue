"""通过正式 Go 入口、真实模型及隔离 SQLite 验证聊天组合链路；不提供模拟上游。"""
import argparse
from contextlib import closing
import datetime
import json
import os
from pathlib import Path
import queue
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid


class Acceptance:
    """只启动和停止本工具持有的托管进程，证据保留在新建目录。"""

    def __init__(self, options):
        self.options = options
        self.root = Path(__file__).resolve().parents[1]
        output = self.root / ".gotmp-chat-real"
        output.mkdir(parents=True, exist_ok=True)
        self.directory = Path(tempfile.mkdtemp(prefix="真实模型-", dir=output))
        self.database = self.directory / "db" / "timecue.db"
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.base = f"http://127.0.0.1:{options.port}"
        self.process = None
        self.log = None
        self.session = str(uuid.uuid4())
        self.evidence = {"provider": options.provider, "model": options.model,
                         "directory": str(self.directory), "runs": [], "checks": []}

    def configure(self, max_output=2048):
        # 不复制用户数据库或包含路径覆盖的配置，密钥仅复制到本次新建目录。
        shutil.copyfile(self.options.env_file, self.directory / ".env")
        config = ("log:\n  level: warn\nchat:\n  max_retry: 0\n"
                  "  compact_threshold: 0.01\nchat_model:\n  - provider: "
                  + json.dumps(self.options.provider) + "\n    base_url: "
                  + json.dumps(self.options.base_url) + "\n    models:\n      - model: "
                  + json.dumps(self.options.model)
                  + f"\n        context_window: 65536\n        max_output_tokens: {max_output}\n")
        (self.directory / "config.yaml").write_text(config, encoding="utf-8")

    def start(self):
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", self.options.port))
        identity = "chat-real-" + uuid.uuid4().hex
        self.log = (self.directory / (identity + ".log")).open("w", encoding="utf-8")
        self.process = subprocess.Popen(
            [str(self.options.go.resolve()), "--data-dir", str(self.directory),
             "--instance-id", identity, "--port", str(self.options.port)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.log,
            text=True, encoding="utf-8", creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        lines = queue.Queue()
        process = self.process
        reader = threading.Thread(target=lambda: lines.put(process.stdout.readline()), daemon=True)
        reader.start()
        ready = json.loads(lines.get(timeout=20))
        assert ready["pid"] == process.pid and ready["instanceId"] == identity, ready
        health = self.request("GET", "/health")[1]["data"]
        assert health["pid"] == process.pid and health["instanceId"] == identity, health
        self.identity = identity
        self.evidence["checks"].append({"ready": identity, "pid": process.pid})

    def stop(self):
        if self.process is None:
            return
        process = self.process
        if process.poll() is None:
            try:
                process.stdin.write('{"command":"shutdown"}\n')
                process.stdin.flush()
                process.wait(timeout=8)
                assert process.returncode == 0, process.returncode
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=5)
        process.stdin.close()
        process.stdout.close()
        self.log.close()
        self.process = None

    def request(self, method, path, body=None, headers=None):
        encoded = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(self.base + path, encoded,
                                        {"Content-Type": "application/json", **(headers or {})}, method=method)
        with self.opener.open(request, timeout=10) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else None

    def rows(self, sql, parameters=()):
        with closing(sqlite3.connect(f"file:{self.database.as_posix()}?mode=ro", uri=True)) as database:
            database.row_factory = sqlite3.Row
            return [dict(row) for row in database.execute(sql, parameters)]

    def wait(self, predicate, description):
        deadline = time.monotonic() + self.options.timeout
        while time.monotonic() < deadline:
            result = predicate()
            if result:
                return result
            assert self.process.poll() is None, "自有 Go 提前退出"
            latest = self.evidence["runs"][-1]
            run = self.request("GET", "/v1/run/" + latest["run_id"])[1]
            if run["finished_at"] is not None:
                result = predicate()
                assert result, (description, run)
                return result
            time.sleep(0.25)
        raise AssertionError("等待超时：" + description)

    def create(self, text):
        key = "create_session:" + str(uuid.uuid4())
        body = {"session_id": self.session, "input": text,
                "provider": self.options.provider, "model": self.options.model}
        status, receipt = self.request("POST", "/v1/run", body, {"Idempotency-Key": key})
        assert status == 202 and set(receipt) == {"run_id", "status", "output", "error"}, receipt
        assert receipt["status"] == "running" and receipt["output"] is None and receipt["error"] is None
        self.evidence["runs"].append({"run_id": receipt["run_id"], "input": text, "key": key})
        return receipt["run_id"]

    def terminal(self, run_id, expected="succeeded"):
        run = self.wait(lambda: (r if (r := self.request("GET", "/v1/run/" + run_id)[1])["finished_at"] else None),
                        "运行终态 " + run_id)
        assert run["status"] == expected, run
        assert run["provider"] == self.options.provider and run["model"] == self.options.model
        events, cursor = [], 0
        while True:
            page = self.request("GET", f"/v1/run/{run_id}/events?after_seq={cursor}&limit=2")[1]
            assert set(page) == {"events", "next_after_seq", "has_more"}
            for event in page["events"]:
                assert set(event) == {"seq", "type", "data"} and event["seq"] == cursor + 1
                cursor = event["seq"]
                events.append(event)
            assert page["next_after_seq"] == cursor
            if not page["has_more"]:
                break
        assert events[0]["type"] == "start" and events[-1]["type"] == "end"
        assert sum(event["type"] == "end" for event in events) == 1
        assert self.request("GET", f"/v1/run/{run_id}/event")[0] == 204
        calls = self.rows("SELECT id,status,model_reply_id,usage FROM ai_model_calls WHERE run_id=?", (run_id,))
        assert calls and any(call["model_reply_id"] for call in calls), calls
        entry = next(item for item in self.evidence["runs"] if item["run_id"] == run_id)
        entry.update({"run": run, "events": events, "model_calls": calls})
        print("PASS", expected, run_id, "events=", len(events), flush=True)
        return events

    def stream_event(self, run_id, cursor):
        request = urllib.request.Request(self.base + f"/v1/run/{run_id}/event",
                                        headers={"Last-Event-ID": str(cursor)})
        with self.opener.open(request, timeout=self.options.timeout) as stream:
            frame = {}
            while line := stream.readline().decode("utf-8").strip():
                if ":" in line:
                    key, value = line.split(":", 1)
                    frame[key] = value.strip()
            assert int(frame["id"]) == cursor + 1, frame
            return frame

    def has_event(self, run_id, kinds):
        page = self.request("GET", f"/v1/run/{run_id}/events?limit=100")[1]
        return any(event["type"] in kinds for event in page["events"])

    def execute(self):
        self.configure()
        self.start()
        assert self.request("GET", "/v1/session")[1] == {"sessions": []}
        assert self.rows("PRAGMA user_version")[0]["user_version"] == 3
        models = self.request("GET", "/v1/model")[1]["models"]
        assert {"provider": self.options.provider, "model": self.options.model, "available": True} in models
        self.terminal(self.create("请简短介绍你能如何帮助管理日程，不要调用工具。"))
        name = "T18真实验收-" + uuid.uuid4().hex[:8]
        planned = (datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8)))
                   + datetime.timedelta(days=2)).replace(hour=9, minute=0, second=0, microsecond=0)
        self.terminal(self.create(f"请立即创建一条单次日程，名称“{name}”，时间 {planned.isoformat()}，说明“真实模型链路”。信息完整，无需再次确认。"))
        schedule = self.rows("SELECT * FROM schedules WHERE name=? AND deleted=0", (name,))
        assert len(schedule) == 1, schedule
        schedule_id = schedule[0]["id"]
        assert schedule[0]["single_notify_time"] == int(planned.timestamp()) * 1_000_000_000
        assert self.rows("SELECT * FROM notifications WHERE schedule_id=? AND deleted=0", (schedule_id,))
        self.terminal(self.create(f"请调用工具查询名称包含“{name}”的列表，查询 id={schedule_id} 的详情，并查询 {planned.date()} 的安排，核对这条日程。"))
        changed = planned + datetime.timedelta(hours=1)
        self.terminal(self.create(f"请先查询最新版本，然后将 id={schedule_id} 的时间修改为 {changed.isoformat()}，说明改为“真实修改已核对”，其他字段保留。直接执行。"))
        updated = self.rows("SELECT * FROM schedules WHERE id=?", (schedule_id,))[0]
        assert updated["revision"] == 2 and updated["single_notify_time"] == int(changed.timestamp()) * 1_000_000_000, updated
        reminders = self.rows("SELECT * FROM notifications WHERE schedule_id=? AND deleted=0", (schedule_id,))
        assert len(reminders) == 1 and reminders[0]["notify_time"] == updated["single_notify_time"], reminders
        self.terminal(self.create(f"请先查询 id={schedule_id} 的最新版本，再直接删除该日程。"))
        deleted = self.rows("SELECT deleted,revision FROM schedules WHERE id=?", (schedule_id,))[0]
        assert deleted == {"deleted": 1, "revision": 3}, deleted
        assert not self.rows("SELECT id FROM notifications WHERE schedule_id=? AND deleted=0 AND delivery_stopped=0", (schedule_id,))
        tools = self.rows("SELECT name,status,idempotency_key FROM ai_tool_calls")
        assert {"create_schedule", "update_schedule", "delete_schedule", "list_schedules", "get_schedule", "query_occurrences"} <= {t["name"] for t in tools if t["status"] == "succeeded"}, tools
        self.evidence["schedule"] = {"id": schedule_id, "created": schedule[0], "updated": updated, "deleted": deleted, "tools": tools}
        self.cancel_and_resume()
        self.evidence["checks"].append({"compacts": self.rows("SELECT run_id,compact_succeeded FROM ai_events WHERE type='compact'")})
        assert self.rows("SELECT id FROM ai_events WHERE type='compact' AND compact_succeeded=1"), "未取得真实摘要成功证据"
        self.flutter()
        self.evidence["passed"] = True

    def cancel_and_resume(self):
        # 使用新会话避免摘要先于长输出；只按持久化进度同步，不伪造模型响应。
        history_session = self.session
        self.session = str(uuid.uuid4())
        run_id = self.create("请用中文连续编号写300条不同的日程管理建议，每条至少20字。不要工具，不要省略。")
        first = self.stream_event(run_id, 0)
        second = self.stream_event(run_id, int(first["id"]))
        self.evidence["checks"].append({"sse_reconnect": run_id, "frames": [first, second]})
        self.wait(lambda: self.has_event(run_id, {"text", "thinking"}), "真实流式增量")
        status, receipt = self.request("POST", f"/v1/run/{run_id}/cancel")
        assert status == 200 and receipt == {"run_id": run_id, "status": "cancelled"}
        self.terminal(run_id, "cancelled")
        self.session = str(uuid.uuid4())
        run_id = self.create("请逐行输出1到120的数字，每行一个数字，不要省略，不要调用工具。")
        self.wait(lambda: self.has_event(run_id, {"text", "thinking"}), "模型退出前真实增量")
        self.stop()
        assert self.rows("SELECT status FROM ai_runs WHERE run_id=?", (run_id,))[0]["status"] == "running"
        original_model = self.rows("SELECT id,input,retry_count FROM ai_model_calls WHERE run_id=? AND status='running'", (run_id,))[0]
        self.start()
        self.terminal(run_id)
        recovered_model = self.rows("SELECT input,retry_count FROM ai_model_calls WHERE id=?", (original_model["id"],))[0]
        assert recovered_model == {"input": original_model["input"], "retry_count": original_model["retry_count"]}
        self.evidence["checks"].append({"model_recovery": run_id, "original_call": original_model["id"]})
        self.session = history_session
        # 低阈值使真实历史进入摘要；在摘要请求或部分响应阶段正常退出。
        run_id = self.create("请简短回顾此前真实创建、修改和删除的日程，并说明当前是否还存在。")
        self.wait(lambda: self.has_event(run_id, {"compact"}), "实际摘要请求")
        self.stop()
        original = self.rows(
            "SELECT c.model_call_id,m.input,m.retry_count FROM ai_checkpoints c JOIN ai_model_calls m ON m.id=c.model_call_id WHERE c.run_id=? AND c.status='compact' AND m.status='running'", (run_id,))[0]
        record = self.rows("SELECT status,finished_at FROM ai_runs WHERE run_id=?", (run_id,))[0]
        assert record == {"status": "running", "finished_at": None}, record
        self.start()
        self.terminal(run_id)
        restored = self.rows("SELECT input,retry_count FROM ai_model_calls WHERE id=?", (original["model_call_id"],))[0]
        assert restored["input"] == original["input"] and restored["retry_count"] == original["retry_count"]
        self.evidence["checks"].append({"summary_recovery": run_id, "original_call": original["model_call_id"]})
        saved = self.request("GET", "/v1/session/" + self.session)[1]
        self.stop()
        self.start()
        assert self.request("GET", "/v1/session/" + self.session)[1] == saved
        for entry in self.evidence["runs"]:
            assert self.request("GET", "/v1/run/" + entry["run_id"])[1] == entry["run"]

    def flutter(self):
        if not self.options.flutter_test:
            return
        manifest = self.directory / "flutter-input.json"
        manifest.write_text(json.dumps({"base": self.base, "instance": self.identity,
                                       "runs": [entry["run"] for entry in self.evidence["runs"]],
                                       "events": {entry["run_id"]: entry["events"] for entry in self.evidence["runs"]}}), encoding="utf-8")
        environment = {**os.environ, "TIMECUE_REAL_CHAT_INPUT": str(manifest)}
        flutter = shutil.which("flutter.bat" if os.name == "nt" else "flutter")
        assert flutter, "找不到 Flutter CLI"
        subprocess.run([flutter, "test", "tool/chat_real_http_smoke_test.dart"],
                       cwd=self.root / "desktop", env=environment, check=True, timeout=180)
        self.evidence["checks"].append({"flutter_real_http": True})

    def save(self):
        path = self.directory / "result.json"
        path.write_text(json.dumps(self.evidence, ensure_ascii=False, indent=2), encoding="utf-8")
        print("本地证据：", path, flush=True)


def main():
    """解析显式模型与密钥路径，在任何失败时保存证据并停止自有进程。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", type=Path, required=True)
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--provider", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--port", type=int, default=1145)
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--flutter-test", action="store_true")
    acceptance = Acceptance(parser.parse_args())
    try:
        acceptance.execute()
    finally:
        try:
            acceptance.stop()
        finally:
            acceptance.save()


if __name__ == "__main__":
    main()
