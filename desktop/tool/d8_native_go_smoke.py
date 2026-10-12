"""用生产 ManagedProcess 驱动真实 Go，所有数据库均为新建的隔离夹具。"""
import argparse
import ctypes
import json
import os
import queue
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.request
import uuid
from pathlib import Path

root = Path(__file__).resolve().parents[2]
work = root / ".gotmp-d8"
work.mkdir(exist_ok=True)
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--go", type=Path, required=True)
parser.add_argument("--driver", type=Path, required=True)
options = parser.parse_args()
driver, binary = options.driver.resolve(), options.go.resolve()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
kernel = ctypes.WinDLL("kernel32", use_last_error=True)
kernel.OpenProcess.argtypes = [ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
kernel.OpenProcess.restype = ctypes.c_void_p
kernel.WaitForSingleObject.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
kernel.CloseHandle.argtypes = [ctypes.c_void_p]
directory = Path(tempfile.mkdtemp(prefix="D8 中文 空格-", dir=work))
db = directory / "fixture.db"
with sqlite3.connect(db) as connection:
    for name in ("001_initial.up.sql", "002_schedule_changes.up.sql"):
        connection.executescript((root / "migrations" / name).read_text(encoding="utf-8-sig"))
(directory / "config.yaml").write_text("db:\n  db_path: ./fixture.db\nlog:\n  log_path: ./logs\n", encoding="utf-8")
evidence = {"directory": str(directory), "binary": str(binary), "runs": []}

def request(path, data=None):
    content = None if data is None else json.dumps(data).encode()
    req = urllib.request.Request("http://127.0.0.1:10086" + path, data=content,
                                 headers={"Content-Type": "application/json"})
    with opener.open(req, timeout=3) as response:
        return json.load(response)

def launch():
    identity = "d8-native-" + uuid.uuid4().hex
    error = open(directory / (identity + ".stderr.txt"), "w", encoding="utf-8")
    start = time.monotonic()
    host = subprocess.Popen([str(driver), "--go", str(binary), str(directory), identity],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=error,
                            encoding="utf-8", text=True, creationflags=subprocess.CREATE_NO_WINDOW)
    lines = queue.Queue()
    threading.Thread(target=lambda: lines.put(host.stdout.readline()), daemon=True).start()
    try:
        ready = json.loads(lines.get(timeout=10))
        assert ready["instanceId"] == identity and ready["event"] == "ready"
        assert ready["pid"] != host.pid
        health = request("/health")["data"]
        assert health["status"] == "ok"
        for key in ("instanceId", "pid", "apiVersion", "capabilities"):
            assert health[key] == ready[key]
        handle = kernel.OpenProcess(0x00100000 | 0x1000, False, ready["pid"])
        assert handle, ctypes.get_last_error()
        item = {"ownerPid": host.pid, "ready": ready, "health": health,
                "startupMilliseconds": round((time.monotonic() - start) * 1000)}
        evidence["runs"].append(item)
        return host, error, handle, item
    except BaseException:
        host.kill()
        host.wait(timeout=5)
        error.close()
        raise

try:
    host, error, handle, item = launch()
    start = time.monotonic()
    host.stdin.write("shutdown\n")
    host.stdin.flush()
    assert host.wait(timeout=5) == 0
    assert kernel.WaitForSingleObject(handle, 5000) == 0
    item["shutdownMilliseconds"] = round((time.monotonic() - start) * 1000)
    item["stopped"] = True
    kernel.CloseHandle(handle)
    error.close()
    host, error, handle, item = launch()
    start = time.monotonic()
    host.kill()  # 只结束本脚本持有创建句柄的原生宿主。
    host.wait(timeout=5)
    assert kernel.WaitForSingleObject(handle, 5000) == 0
    item["ownerCrashCleanupMilliseconds"] = round((time.monotonic() - start) * 1000)
    item["jobStoppedGo"] = True
    kernel.CloseHandle(handle)
    error.close()
    host, error, handle, item = launch()
    assert evidence["runs"][0]["ready"]["instanceId"] != item["ready"]["instanceId"]
    host.stdin.close()
    assert host.wait(timeout=5) == 0
    assert kernel.WaitForSingleObject(handle, 5000) == 0
    item["parentEofStopped"] = True
    kernel.CloseHandle(handle)
    error.close()
    with sqlite3.connect(db) as connection:
        evidence["userVersion"] = connection.execute("PRAGMA user_version").fetchone()[0]
        evidence["tables"] = [row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")]
    assert evidence["userVersion"] == 2 and len(evidence["tables"]) == 4
    evidence["passed"] = True
finally:
    if "host" in locals() and host.poll() is None:
        host.kill()
        host.wait(timeout=5)
    (work / "native-go-evidence.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"passed": True, "runs": len(evidence["runs"]), "evidence": str(work / "native-go-evidence.json")}, ensure_ascii=False))
