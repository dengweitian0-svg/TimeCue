"""验证完整发行目录的首次运行、持久化、重复启动和宿主崩溃清理。"""
import argparse
import ctypes
from ctypes import wintypes
import datetime
import json
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--package", type=Path, required=True)
options = parser.parse_args()
package = options.package.resolve()
app = package / "timecue_desktop.exe"
assert app.is_file() and (package / "timecue-core.exe").is_file()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
kernel = ctypes.WinDLL("kernel32", use_last_error=True)
kernel.OpenProcess.argtypes = [ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
kernel.OpenProcess.restype = ctypes.c_void_p
kernel.WaitForSingleObject.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
kernel.CloseHandle.argtypes = [ctypes.c_void_p]


class ProcessEntry(ctypes.Structure):
    """Toolhelp 进程快照，避免通过 WMI 查询自有 Go 的父进程。"""

    _fields_ = [("dwSize", wintypes.DWORD), ("cntUsage", wintypes.DWORD),
                ("th32ProcessID", wintypes.DWORD), ("th32DefaultHeapID", ctypes.c_size_t),
                ("th32ModuleID", wintypes.DWORD), ("cntThreads", wintypes.DWORD),
                ("th32ParentProcessID", wintypes.DWORD), ("pcPriClassBase", wintypes.LONG),
                ("dwFlags", wintypes.DWORD), ("szExeFile", wintypes.WCHAR * 260)]


kernel.CreateToolhelp32Snapshot.argtypes = [wintypes.DWORD, wintypes.DWORD]
kernel.CreateToolhelp32Snapshot.restype = wintypes.HANDLE
kernel.Process32FirstW.argtypes = [wintypes.HANDLE, ctypes.POINTER(ProcessEntry)]
kernel.Process32NextW.argtypes = [wintypes.HANDLE, ctypes.POINTER(ProcessEntry)]


def parent_process_id(pid):
    """只读快照核对健康响应 PID 的实际父进程。"""
    snapshot = kernel.CreateToolhelp32Snapshot(2, 0)
    assert snapshot != wintypes.HANDLE(-1).value, "无法读取进程快照"
    try:
        entry = ProcessEntry()
        entry.dwSize = ctypes.sizeof(entry)
        more = kernel.Process32FirstW(snapshot, ctypes.byref(entry))
        while more:
            if entry.th32ProcessID == pid:
                return entry.th32ParentProcessID
            more = kernel.Process32NextW(snapshot, ctypes.byref(entry))
        raise AssertionError("健康响应进程已退出")
    finally:
        kernel.CloseHandle(snapshot)


with socket.socket() as probe:
    probe.bind(("127.0.0.1", 10086))

# 所有写入限定在新建隔离目录，测试只结束自己创建的宿主。
evidence_root = Path(__file__).resolve().parents[1] / ".gotmp-release-smoke"
evidence_root.mkdir(exist_ok=True)
work = Path(tempfile.mkdtemp(prefix="TimeCue 发行验证 ", dir=evidence_root))
data = work / "中文 数据目录"


def request(path, payload=None):
    body = None if payload is None else json.dumps(payload).encode()
    query = urllib.request.Request("http://127.0.0.1:10086" + path, body, {"Content-Type": "application/json"})
    with opener.open(query, timeout=2) as response:
        return json.load(response)["data"]


def health(host):
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        if host.poll() is not None:
            raise AssertionError("发行宿主提前退出")
        try:
            result = request("/health")
        except Exception:
            time.sleep(0.2)
            continue
        pid = int(result["pid"])
        assert parent_process_id(pid) == host.pid, "响应来自其他服务"
        assert result["status"] == "ok"
        return result
    raise AssertionError("发行宿主未在预算内完成首次运行")


results = []
schedule_id = None
for run in range(2):
    log = open(work / f"host-{run}.log", "w", encoding="utf-8")
    host = subprocess.Popen([str(app), "--data-dir=" + str(data), "--start-hidden"],
                            cwd=work, stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
    child_handle = None
    try:
        runtime = health(host)
        child_handle = kernel.OpenProcess(0x00100000, False, runtime["pid"])
        assert child_handle, "无法观察自有 Go 退出"
        duplicate = subprocess.run([str(app), "--start-hidden"], cwd=work, timeout=5,
                                   creationflags=subprocess.CREATE_NO_WINDOW)
        assert duplicate.returncode == 0 and host.poll() is None
        assert request("/health")["instanceId"] == runtime["instanceId"]
        if run == 0:
            planned = (datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))) + datetime.timedelta(days=1)).replace(second=0, microsecond=0)
            schedule_id = request("/api/v1/scheduler/schedules", {
                "name": "发行持久化验证", "type": "single", "description": None,
                "single": {"notifyTime": planned.isoformat()},
            })["id"]
        else:
            assert request(f"/api/v1/scheduler/schedules/{schedule_id}")["name"] == "发行持久化验证"
        with sqlite3.connect(data / "db/timecue.db") as database:
            assert database.execute("PRAGMA user_version").fetchone()[0] == 3
            assert database.execute("SELECT COUNT(*) FROM ai_sessions").fetchone()[0] == 0
        results.append({"run": run, "hostPid": host.pid, "goPid": runtime["pid"], "instanceId": runtime["instanceId"]})
    finally:
        if host.poll() is None:
            host.terminate()
            host.wait(timeout=5)
        if child_handle:
            try:
                assert kernel.WaitForSingleObject(child_handle, 5000) == 0, "宿主退出后 Go 未被 Job 回收"
            finally:
                kernel.CloseHandle(child_handle)
        log.close()

assert results[0]["instanceId"] != results[1]["instanceId"]
(work / "result.json").write_text(json.dumps({"package": str(package), "runs": results, "scheduleId": schedule_id}, ensure_ascii=False, indent=2), encoding="utf-8")
print("PASS 完整发行目录：首次运行、中文路径、数据保留、重复启动和 Job 清理")
print("证据：", work / "result.json")
