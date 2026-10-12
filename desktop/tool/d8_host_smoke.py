"""真实 Flutter/Windows/Go 联调；只操作自有测试进程及新建数据库。"""
import argparse
import ctypes
import datetime
import json
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

root = Path(__file__).resolve().parents[2]
work = root / ".gotmp-d8"
work.mkdir(exist_ok=True)
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--go", type=Path, required=True)
parser.add_argument("--app", type=Path, default=root / "desktop/build/windows/x64/runner/Release/timecue_desktop.exe")
options = parser.parse_args()
binary, app = options.go.resolve(), options.app.resolve()
kernel = ctypes.WinDLL("kernel32", use_last_error=True)
kernel.OpenProcess.argtypes = [ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
kernel.OpenProcess.restype = ctypes.c_void_p
kernel.WaitForSingleObject.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
kernel.CloseHandle.argtypes = [ctypes.c_void_p]
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
summary = {"runs": []}

def fixture(mode):
    directory = Path(tempfile.mkdtemp(prefix="D8 宿主 " + mode + "-", dir=work))
    planned = int(datetime.datetime.now(datetime.timezone.utc).replace(second=0, microsecond=0).timestamp() - 120) * 1_000_000_000
    created = planned - 3_600_000_000_000
    with sqlite3.connect(directory / "fixture.db") as connection:
        for name in ("001_initial.up.sql", "002_schedule_changes.up.sql"):
            connection.executescript((root / "migrations" / name).read_text(encoding="utf-8-sig"))
        connection.execute("INSERT INTO schedules (id,name,type,single_notify_time,created_at,updated_at,revision) VALUES (42,'D8 隔离联调','single',?,?,?,1)", (planned, created, created))
        connection.execute("INSERT INTO schedule_versions (schedule_id,revision,effective_from,name,type,single_notify_time,cancelled,deleted) VALUES (42,1,?,'D8 隔离联调','single',?,0,0)", (created, planned))
    (directory / "config.yaml").write_text("db:\n  db_path: ./fixture.db\nlog:\n  log_path: ./logs\n", encoding="utf-8")
    return directory

def health():
    with opener.open("http://127.0.0.1:10086/health", timeout=1) as response:
        return json.load(response)["data"]

for mode in ("managed", "external"):
    directory = fixture(mode)
    manual = None
    go_handle = None
    log = open(directory / "flutter-output.txt", "w", encoding="utf-8")
    try:
        if mode == "external":
            manual = subprocess.Popen([str(binary), "--data-dir", str(directory), "--instance-id", "d8-external-fixture"],
                                      stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                      encoding="utf-8", text=True, creationflags=subprocess.CREATE_NO_WINDOW)
            ready = json.loads(manual.stdout.readline())
            assert ready["pid"] == manual.pid
        args = [str(app), "--backend=" + mode, "--data-dir=" + str(directory), "--start-hidden"]
        if mode == "managed":
            args.append("--go-executable=" + str(binary))
        start = time.monotonic()
        host = subprocess.Popen(args, stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
        deadline = time.monotonic() + 20
        while host.poll() is None and time.monotonic() < deadline:
            ready_file = directory / "d8-host-ready.json"
            continue_file = directory / "d8-host-continue"
            if ready_file.exists() and not continue_file.exists():
                item = json.loads(ready_file.read_text(encoding="utf-8"))
                assert health()["pid"] == item["pid"]
                if mode == "managed":
                    go_handle = kernel.OpenProcess(0x00100000 | 0x1000, False, item["pid"])
                    assert go_handle
                continue_file.write_text("continue", encoding="utf-8")
            time.sleep(0.03)
        if host.poll() is None:
            host.kill()
            host.wait(timeout=5)
            raise RuntimeError("Flutter 宿主 20 秒内未完成隔离闭环")
        assert host.returncode == 0, host.returncode
        evidence = json.loads((directory / "d8-host-evidence.json").read_text(encoding="utf-8"))
        assert evidence["passed"], evidence
        assert evidence["sent"] == 1 and evidence["unacknowledged"] == 1 and evidence["healthMatched"]
        if mode == "managed":
            assert go_handle and kernel.WaitForSingleObject(go_handle, 5000) == 0
            evidence["ownedGoExited"] = True
        else:
            assert manual.poll() is None and health()["pid"] == manual.pid
            evidence["manualGoStillAlive"] = True
        with sqlite3.connect(directory / "fixture.db") as connection:
            row = connection.execute("SELECT status,acknowledged_at FROM notifications WHERE schedule_id=42").fetchone()
            assert row == ("sent", None), row
            evidence["databaseResult"] = list(row)
        evidence["hostElapsedMilliseconds"] = round((time.monotonic() - start) * 1000)
        evidence["directory"] = str(directory)
        summary["runs"].append(evidence)
        print(json.dumps(evidence, ensure_ascii=False), flush=True)
    finally:
        if "host" in locals() and host.poll() is None:
            host.kill()
            host.wait(timeout=5)
        if manual and manual.poll() is None:
            manual.stdin.write('{"command":"shutdown"}\n')
            manual.stdin.flush()
            manual.wait(timeout=5)
        if go_handle:
            kernel.CloseHandle(go_handle)
        log.close()
        (work / "flutter-host-evidence.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
assert len(summary["runs"]) == 2
