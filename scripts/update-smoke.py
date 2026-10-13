#!/usr/bin/env python3
"""真实启动托管 TimeCue，验证 Windows PowerShell 更新、重启和事务恢复。"""
import argparse
import ctypes
import contextlib
from ctypes import wintypes
import datetime as dt
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request


PORT = 10086
CREATE_NO_WINDOW = 0x08000000
SYNCHRONIZE = 0x00100000
PROCESS_TERMINATE = 0x0001
PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
WAIT_OBJECT_0 = 0
WAIT_TIMEOUT = 258
ERROR_INVALID_PARAMETER = 87
VERSION_RE = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\+(0|[1-9]\d*)$")
PACKAGE_RE = re.compile(r"^TimeCue-(\d+\.\d+\.\d+\+\d+)-windows-x64-(\d{8}-\d{6}-\d{7})$")
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))
KERNEL = None
SMOKE_ROOT = None
SMOKE_EVIDENCE = None
if os.name == "nt":
    KERNEL = ctypes.WinDLL("kernel32", use_last_error=True)
    KERNEL.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    KERNEL.OpenProcess.restype = wintypes.HANDLE
    KERNEL.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
    KERNEL.WaitForSingleObject.restype = wintypes.DWORD
    KERNEL.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
    KERNEL.TerminateProcess.restype = wintypes.BOOL
    KERNEL.QueryFullProcessImageNameW.argtypes = [
        wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)
    ]
    KERNEL.QueryFullProcessImageNameW.restype = wintypes.BOOL
    KERNEL.CloseHandle.argtypes = [wintypes.HANDLE]
    KERNEL.CloseHandle.restype = wintypes.BOOL


def powershell_info(executable, pid):
    command = (
        f"$p=Get-CimInstance Win32_Process -Filter 'ProcessId={int(pid)}'; "
        "if($null -eq $p){exit 3}; "
        "$p | Select-Object ProcessId,ParentProcessId,ExecutablePath | ConvertTo-Json -Compress"
    )
    result = subprocess.run(
        [executable, "-NoProfile", "-NonInteractive", "-Command", command],
        capture_output=True,
        text=True,
        timeout=20,
        creationflags=CREATE_NO_WINDOW,
    )
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)


def powershell_find_image(executable, image):
    escaped = str(image).replace("'", "''")
    command = (
        f"$p=Get-CimInstance Win32_Process | Where-Object {{ $_.ExecutablePath -and "
        f"$_.ExecutablePath -ieq '{escaped}' }} | "
        "Select-Object ProcessId,ParentProcessId,ExecutablePath; "
        "ConvertTo-Json -InputObject @($p) -Compress"
    )
    result = subprocess.run(
        [executable, "-NoProfile", "-NonInteractive", "-Command", command],
        capture_output=True,
        text=True,
        timeout=20,
        creationflags=CREATE_NO_WINDOW,
    )
    if result.returncode != 0:
        raise RuntimeError(f"无法查询自建宿主：{result.stderr[-1000:]}")
    value = json.loads(result.stdout or "[]")
    return value if isinstance(value, list) else [value]


def open_expected_process(pid, expected_image, powershell):
    """先核对 CIM 路径，再持有只针对指定 PID 的内核进程句柄。"""
    info = powershell_info(powershell, pid)
    if info is None or not same_path(info.get("ExecutablePath", ""), expected_image):
        raise RuntimeError(f"进程{pid}路径与预期不符：{info}")
    access = SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE
    handle = KERNEL.OpenProcess(access, False, int(pid))
    if not handle:
        raise ctypes.WinError(ctypes.get_last_error())
    image = ctypes.create_unicode_buffer(32768)
    length = wintypes.DWORD(len(image))
    if not KERNEL.QueryFullProcessImageNameW(handle, 0, image, ctypes.byref(length)):
        error = ctypes.get_last_error()
        KERNEL.CloseHandle(handle)
        raise ctypes.WinError(error)
    if not same_path(image.value, expected_image):
        KERNEL.CloseHandle(handle)
        raise RuntimeError(f"进程{pid}内核路径与预期不符：{image.value}")
    return handle, info


def same_path(left, right):
    return os.path.normcase(os.path.abspath(str(left))) == os.path.normcase(os.path.abspath(str(right)))


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def wait_handle(handle, timeout, label):
    result = KERNEL.WaitForSingleObject(handle, int(timeout * 1000))
    if result == WAIT_OBJECT_0:
        return
    if result == WAIT_TIMEOUT:
        raise RuntimeError(f"等待{label}退出超时")
    raise ctypes.WinError(ctypes.get_last_error())


def terminate_expected(handle, pid, expected_image, powershell):
    if KERNEL.WaitForSingleObject(handle, 0) == WAIT_OBJECT_0:
        return
    info = powershell_info(powershell, pid)
    if info is None:
        return
    if not same_path(info.get("ExecutablePath", ""), expected_image):
        raise RuntimeError(f"拒绝结束路径不匹配的进程{pid}")
    if not KERNEL.TerminateProcess(handle, 1):
        error = ctypes.get_last_error()
        if error != ERROR_INVALID_PARAMETER:
            raise ctypes.WinError(error)
    wait_handle(handle, 10, f"自建进程{pid}")


def terminate_exact_image(image, powershell, parent_pid=None):
    """仅遍历本次唯一临时安装路径，必要时再核对父PID。"""
    for info in powershell_find_image(powershell, image):
        pid = int(info["ProcessId"])
        if parent_pid is not None and int(info["ParentProcessId"]) != parent_pid:
            continue
        handle, _ = open_expected_process(pid, image, powershell)
        try:
            terminate_expected(handle, pid, image, powershell)
        finally:
            KERNEL.CloseHandle(handle)


def check_port_free():
    probe = socket.socket()
    try:
        probe.bind(("127.0.0.1", PORT))
    except OSError as error:
        raise RuntimeError(f"端口{PORT}已占用；smoke拒绝触碰现有实例：{error}") from error
    finally:
        probe.close()


def request(path, payload=None):
    body = None if payload is None else json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{PORT}{path}",
        body,
        {"Content-Type": "application/json"},
    )
    with HTTP.open(req, timeout=3) as response:
        envelope = json.load(response)
    if not isinstance(envelope, dict) or "data" not in envelope:
        raise RuntimeError(f"API响应格式无效：{envelope}")
    return envelope["data"]


def wait_health(host_pid, old_instance=None, timeout=45):
    deadline = time.monotonic() + timeout
    last_error = "尚未收到health响应"
    expected_host = INSTALL_APP
    expected_go = INSTALL_APP.parent / "timecue-core.exe"
    while time.monotonic() < deadline:
        try:
            health = request("/health")
            child_pid = int(health["pid"])
            host_info = powershell_info(POWERSHELL, host_pid)
            if host_info is None:
                raise RuntimeError(
                    f"宿主进程信息缺失：expected PID={host_pid}, path={expected_host}; actual=None"
                )
            actual_host_path = host_info.get("ExecutablePath") or "<无路径>"
            if not same_path(actual_host_path, expected_host):
                raise RuntimeError(
                    f"宿主映像路径不匹配：expected PID={host_pid}, path={expected_host}; "
                    f"actual PID={host_info.get('ProcessId')}, path={actual_host_path}"
                )

            go_info = powershell_info(POWERSHELL, child_pid)
            if go_info is None:
                raise RuntimeError(
                    f"health Go 进程信息缺失：expected PID={child_pid}, "
                    f"parent PID={host_pid}, path={expected_go}; actual=None"
                )
            actual_go_path = go_info.get("ExecutablePath") or "<无路径>"
            actual_parent_pid = go_info.get("ParentProcessId")
            try:
                actual_parent_pid = int(actual_parent_pid)
            except (TypeError, ValueError):
                actual_parent_pid = None
            if not same_path(actual_go_path, expected_go) or actual_parent_pid != host_pid:
                raise RuntimeError(
                    f"health Go 身份不匹配：expected PID={child_pid}, parent PID={host_pid}, "
                    f"path={expected_go}; actual PID={go_info.get('ProcessId')}, "
                    f"parent PID={actual_parent_pid}, path={actual_go_path}"
                )
            if health.get("status") != "ok":
                raise RuntimeError(f"health status异常：expected=ok; actual={health.get('status')!r}")
            if old_instance is not None and health.get("instanceId") == old_instance:
                raise RuntimeError(f"health instanceId未更新：old={old_instance!r}, actual={health.get('instanceId')!r}")
            return health
        except Exception as error:  # 启动期间轮询，只保留最后一个诊断。
            last_error = str(error)
            time.sleep(0.25)
    raise RuntimeError(f"托管服务未就绪：{last_error}")


def read_status(path):
    try:
        return json.loads(path.read_text(encoding="utf-8-sig"))
    except (FileNotFoundError, json.JSONDecodeError):
        return None


def bounded_log_tail(path, limit=16 * 1024):
    """读取隔离 smoke 日志末尾的有界文本，用于保留失败诊断。"""
    try:
        with Path(path).open("rb") as stream:
            stream.seek(0, os.SEEK_END)
            size = stream.tell()
            stream.seek(max(0, size - limit), os.SEEK_SET)
            return stream.read(limit).decode("utf-8", errors="replace")
    except OSError as error:
        return f"无法读取隔离日志：{error}"


def helper_error_summary(log_tail):
    """从更新器日志尾部提取一条有界错误摘要。"""
    if not log_tail:
        return ""
    lines = [line.strip() for line in log_tail.splitlines() if line.strip()]
    if not lines:
        return ""
    markers = ("失败", "错误", "exception", "error", "failed", "not recognized")
    for line in reversed(lines):
        if any(marker in line.lower() for marker in markers):
            return line[-1200:]
    return lines[-1][-1200:]


def wait_status(path, states, timeout, helper=None):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = read_status(path)
        if last and last.get("state") in states:
            return last
        if helper is not None and helper.poll() is not None:
            raise RuntimeError(
                f"更新助手提前退出({helper.returncode})，状态={last}，日志见临时工作目录"
            )
        time.sleep(0.2)
    raise RuntimeError(f"等待状态{sorted(states)}超时，最后状态={last}")


def check_manifest(package, manifest):
    if manifest.get("platform") != "windows-x64" or not isinstance(manifest.get("files"), list):
        raise RuntimeError("发行manifest平台或files字段无效")
    checked = 0
    for entry in manifest["files"]:
        relative = PurePosixPath(str(entry.get("path", "")))
        if relative.is_absolute() or ".." in relative.parts or not relative.parts:
            raise RuntimeError(f"manifest包含不安全路径：{relative}")
        file_path = package.joinpath(*relative.parts)
        if not file_path.is_file():
            raise RuntimeError(f"发行目录缺少manifest文件：{relative}")
        actual = file_sha256(file_path)
        if actual.lower() != str(entry.get("sha256", "")).lower():
            raise RuntimeError(f"发行目录文件hash不匹配：{relative}")
        checked += 1
    return checked


def check_release_zip(package, manifest):
    archive = package.with_name(package.name + ".zip")
    sidecar = archive.with_name(archive.name + ".sha256")
    if not archive.is_file() or not sidecar.is_file():
        raise RuntimeError("--package旁缺少真实发行ZIP或对应.sha256 sidecar")
    lines = sidecar.read_text(encoding="ascii").splitlines()
    match = re.fullmatch(r"([0-9a-fA-F]{64})\s+\*?(.+)", lines[0] if lines else "")
    if not match or match.group(2).strip() != archive.name:
        raise RuntimeError("发行ZIP的sidecar文件名或格式无效")
    digest = file_sha256(archive)
    if digest.lower() != match.group(1).lower():
        raise RuntimeError("发行ZIP与sidecar SHA-256不匹配")
    return archive, sidecar


def write_plan(path, plan):
    path.write_text(json.dumps(plan, ensure_ascii=False, indent=2), encoding="utf-8")


def start_helper(powershell, helper, mode, plan, log_path, journal=None):
    command = [
        powershell,
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        str(helper),
        "-Mode",
        mode,
    ]
    if journal is None:
        command.extend(["-PlanPath", str(plan)])
    else:
        command.extend(["-JournalPath", str(journal)])
    output = log_path.open("ab", buffering=0)
    process = subprocess.Popen(
        command,
        stdin=subprocess.DEVNULL,
        stdout=output,
        stderr=subprocess.STDOUT,
        creationflags=CREATE_NO_WINDOW,
    )
    return process, output


def run_smoke(package, helper_override):
    global POWERSHELL, INSTALL_APP, SMOKE_ROOT, SMOKE_EVIDENCE
    package = package.resolve()
    if not package.is_dir():
        raise RuntimeError("--package必须指向完整发行目录")
    match = PACKAGE_RE.fullmatch(package.name)
    if not match:
        raise RuntimeError("发行目录名必须是TimeCue-version-windows-x64-timestamp")
    version = match.group(1)
    if not VERSION_RE.fullmatch(version):
        raise RuntimeError("manifest版本必须是主.次.补丁+数字构建号")
    manifest_path = package / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    if manifest.get("version") != version or manifest.get("platform") != "windows-x64":
        raise RuntimeError("发行目录名、manifest版本或平台不匹配")
    checked_files = check_manifest(package, manifest)
    archive, sidecar = check_release_zip(package, manifest)
    helper = Path(helper_override).resolve() if helper_override else package / "timecue-updater.ps1"
    if not helper.is_file():
        raise RuntimeError("找不到install-update.ps1或timecue-updater.ps1")
    POWERSHELL = shutil.which("powershell.exe") or shutil.which("powershell")
    if not POWERSHELL:
        raise RuntimeError("找不到Windows PowerShell 5.1 powershell.exe")
    check_port_free()

    root = Path(tempfile.mkdtemp(prefix="TimeCue 更新隔离验证 ", dir=package.parent)).resolve()
    SMOKE_ROOT = root
    work, install, data, staging = (root / name for name in ("工作区", "程序安装", "中文 用户数据", "解包暂存"))
    for folder in (work, install, data, staging):
        folder.mkdir(parents=True, exist_ok=True)
    shutil.copytree(package, install, dirs_exist_ok=True)
    INSTALL_APP = install / "timecue_desktop.exe"
    go_exe = install / "timecue-core.exe"
    if not INSTALL_APP.is_file() or not go_exe.is_file():
        raise RuntimeError("发行目录缺少真实桌面程序或Go服务")

    old_manifest_bytes = (install / "manifest.json").read_bytes()
    readme = install / "README.md"
    if not readme.is_file():
        raise RuntimeError("发行目录缺少README，无法验证回滚")
    old_readme = "隔离 smoke 旧版 README 标记\n".encode("utf-8")
    readme.write_bytes(old_readme)
    install_unknown = install / "用户未知文件.keep"
    install_unknown.write_text("不得被升级或恢复流程删除\n", encoding="utf-8")

    db_dir, log_dir = data / "用户数据库", data / "用户日志"
    db_dir.mkdir()
    log_dir.mkdir()
    database = db_dir / "主数据库.sqlite"
    config = data / "config.yaml"
    config_bytes = (
        f"log:\n  level: info\n  log_path: {log_dir.as_posix()}\n"
        f"db:\n  db_path: {database.as_posix()}\n"
        "unknown:\n  smoke_marker: preserve-me\n"
    ).encode("utf-8")
    config.write_bytes(config_bytes)
    data_unknown = data / "用户未知文件.keep"
    unknown_marker = "用户未知数据标记\n".encode("utf-8")
    data_unknown.write_bytes(unknown_marker)

    logs = work / "logs"
    logs.mkdir()
    host_log = (logs / "host.log").open("ab", buffering=0)
    helper_log = logs / "updater.log"
    host_process = None
    host_handle = None
    host_pid = None
    go_handle = None
    go_pid = None
    helper_process = None
    helper_output = None
    new_instance = None
    schedule_id = None
    old_instance = None
    evidence = {
        "scope": "PowerShell事务安装、硬退等待、Job回收、完整包替换重启、用户数据库保留与Recover恢复",
        "notificationDeliveryValidated": False,
        "releaseVersion": version,
        "simulatedCurrentVersion": "0.0.0+0",
        "versionCaveat": "测试使用同一构建产物模拟旧版本号；未验证两个正式Release之间的升级。",
        "package": str(package),
        "archive": str(archive),
        "manifestFilesChecked": checked_files,
        "workDirectory": str(root),
        "phases": [],
    }
    SMOKE_EVIDENCE = evidence
    completed = False
    try:
        # 先启动隔离安装副本中的真实托管应用，绝不连接既有实例。
        host_process = subprocess.Popen(
            [
                str(INSTALL_APP),
                "--backend=managed",
                f"--data-dir={data}",
                f"--go-executable={go_exe}",
                "--start-hidden",
            ],
            cwd=work,
            stdin=subprocess.DEVNULL,
            stdout=host_log,
            stderr=subprocess.STDOUT,
            creationflags=CREATE_NO_WINDOW,
        )
        host_pid = host_process.pid
        host_handle, _ = open_expected_process(host_pid, INSTALL_APP, POWERSHELL)
        health = wait_health(host_pid)
        old_instance = str(health["instanceId"])
        go_pid = int(health["pid"])
        go_handle, go_info = open_expected_process(go_pid, go_exe, POWERSHELL)
        if int(go_info["ParentProcessId"]) != host_pid:
            raise RuntimeError("启动服务的进程不是隔离桌面宿主的子进程")
        if not database.is_file() or not any(log_dir.glob("*")):
            raise RuntimeError("托管服务未创建配置指定的数据库或日志")

        planned = (dt.datetime.now(dt.timezone(dt.timedelta(hours=8))) + dt.timedelta(days=2))
        planned = planned.replace(second=0, microsecond=0)
        created = request(
            "/api/v1/scheduler/schedules",
            {
                "name": "更新隔离持久化验证",
                "type": "single",
                "description": "真实托管服务更新 smoke",
                "single": {"notifyTime": planned.isoformat()},
            },
        )
        schedule_id = int(created["id"])
        with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as connection:
            schema_version = connection.execute("PRAGMA user_version").fetchone()[0]
            if schema_version != 2:
                raise RuntimeError(f"SQLite user_version={schema_version}，预期2")
            row = connection.execute("SELECT name FROM schedules WHERE id=?", (schedule_id,)).fetchone()
            if not row or row[0] != "更新隔离持久化验证":
                raise RuntimeError("真实SQLite中没有刚创建的未来单次日程")
        evidence["phases"].append({
            "phase": "managed-bootstrap",
            "hostPid": host_pid,
            "goPid": go_pid,
            "instanceId": old_instance,
            "scheduleId": schedule_id,
            "database": str(database),
            "sqliteUserVersion": schema_version,
        })

        protected = [
            str(config),
            str(data / "software-update-status.json"),
            str(database),
            str(database) + "-wal",
            str(database) + "-shm",
            str(log_dir),
        ]
        base_plan = {
            "schemaVersion": 1,
            "archivePath": str(archive),
            "checksumPath": str(sidecar),
            "stagingDirectory": str(staging),
            "installDirectory": str(install),
            "currentVersion": "0.0.0+0",
            "targetVersion": version,
            "dataDirectory": str(data),
            "parentPid": host_pid,
            "ownedGoPid": go_pid,
            "statusPath": str(data / "software-update-status.json"),
            "protectedPaths": protected,
            "restartArguments": [
                "--backend=managed",
                f"--data-dir={data}",
                f"--go-executable={go_exe}",
                "--start-hidden",
            ],
        }

        # 用损坏副本验证校验失败只影响隔离暂存，不改旧程序和用户数据。
        corrupt = work / "corrupt"
        corrupt.mkdir()
        corrupt_archive = corrupt / archive.name
        shutil.copy2(archive, corrupt_archive)
        with corrupt_archive.open("r+b") as stream:
            stream.seek(-1, os.SEEK_END)
            last = stream.read(1)
            stream.seek(-1, os.SEEK_END)
            stream.write(bytes([last[0] ^ 0xFF]))
            stream.flush()
            os.fsync(stream.fileno())
        corrupt_sidecar = corrupt / sidecar.name
        shutil.copy2(sidecar, corrupt_sidecar)
        corrupt_stage = corrupt / "staging"
        corrupt_stage.mkdir()
        corrupt_plan = dict(base_plan)
        corrupt_plan.update({
            "archivePath": str(corrupt_archive),
            "checksumPath": str(corrupt_sidecar),
            "stagingDirectory": str(corrupt_stage),
            "statusPath": str(data / "software-update-status.json"),
        })
        corrupt_plan_path = corrupt / "plan.json"
        write_plan(corrupt_plan_path, corrupt_plan)
        corrupt_process, corrupt_output = start_helper(
            POWERSHELL, helper, "Prepare", corrupt_plan_path, helper_log
        )
        try:
            corrupt_code = corrupt_process.wait(timeout=180)
        except subprocess.TimeoutExpired:
            corrupt_process.terminate()
            corrupt_process.wait(timeout=10)
            raise RuntimeError("损坏发行包的Prepare校验超时")
        finally:
            corrupt_output.close()
        if corrupt_code == 0:
            raise RuntimeError("损坏发行包的Prepare意外成功")
        if readme.read_bytes() != old_readme or config.read_bytes() != config_bytes:
            raise RuntimeError("拒绝损坏包时修改了旧程序或配置")
        with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as connection:
            if connection.execute("SELECT name FROM schedules WHERE id=?", (schedule_id,)).fetchone() is None:
                raise RuntimeError("拒绝损坏包时修改了用户数据库")
        evidence["phases"].append({"phase": "corrupt-prepare-rejected", "exitCode": corrupt_code})

        # 正式Prepare读取发行生成的ZIP和sidecar，不重新打包伪造发行物。
        actual_plan_path = work / "plan.json"
        write_plan(actual_plan_path, base_plan)
        prepare, prepare_output = start_helper(
            POWERSHELL, helper, "Prepare", actual_plan_path, helper_log
        )
        try:
            prepare_code = prepare.wait(timeout=600)
        except subprocess.TimeoutExpired:
            prepare.terminate()
            prepare.wait(timeout=10)
            raise RuntimeError("发行包Prepare超过10分钟")
        finally:
            prepare_output.close()
        if prepare_code != 0:
            raise RuntimeError(f"真实发行包Prepare失败，退出码{prepare_code}，日志：{helper_log}")
        status_path = data / "software-update-status.json"
        prepared = read_status(status_path)
        if not prepared or prepared.get("state") != "prepared" or prepared.get("targetVersion") != version:
            raise RuntimeError(f"真实发行包Prepare状态无效：{prepared}")
        if host_process.poll() is not None or KERNEL.WaitForSingleObject(go_handle, 0) != WAIT_TIMEOUT:
            raise RuntimeError("正式Prepare阶段意外关闭了原桌面/Go服务")
        if readme.read_bytes() != old_readme:
            raise RuntimeError("Prepare阶段修改了原安装目录文件")
        evidence["phases"].append({"phase": "prepare-complete", "status": prepared})

        # Install必须先ready；确认仍持有原进程，再提交并硬退仅本smoke宿主。
        helper_process, helper_output = start_helper(
            POWERSHELL, helper, "Install", actual_plan_path, helper_log
        )
        ready = wait_status(status_path, {"ready", "failed", "recoveryRequired"}, 90, helper_process)
        if ready.get("state") != "ready":
            raise RuntimeError(f"Install未进入ready：{ready}")
        if host_process.poll() is not None or KERNEL.WaitForSingleObject(host_handle, 0) != WAIT_TIMEOUT:
            raise RuntimeError("ready握手时原宿主已退出")
        if KERNEL.WaitForSingleObject(go_handle, 0) != WAIT_TIMEOUT:
            raise RuntimeError("ready握手时原Go服务已退出")
        if readme.read_bytes() != old_readme:
            raise RuntimeError("commit前程序文件已被替换")
        marker = work / "commit.marker"
        with marker.open("w", encoding="ascii", newline="\n") as stream:
            stream.write("commit\n")
            stream.flush()
            os.fsync(stream.fileno())

        # 精确结束我们创建且已核对路径的宿主，Job应回收它的Go子进程。
        terminate_expected(host_handle, host_pid, INSTALL_APP, POWERSHELL)
        host_process.wait(timeout=10)
        wait_handle(go_handle, 15, "旧托管Go Job进程")
        KERNEL.CloseHandle(go_handle)
        go_handle = None
        KERNEL.CloseHandle(host_handle)
        host_handle = None
        host_process = None
        if helper_process.wait(timeout=180) != 0:
            raise RuntimeError(f"Install助手失败，日志：{helper_log}")
        helper_output.close()
        helper_output = None
        installed = read_status(status_path)
        if not installed or installed.get("state") != "installed" or installed.get("targetVersion") != version:
            raise RuntimeError(f"安装未成功（restartFailed也算失败）：{installed}")

        # 由helper拉起的新桌面必须启动全新Go身份，且Go的父进程为新桌面。
        new_host_deadline = time.monotonic() + 45
        new_host_info = None
        while time.monotonic() < new_host_deadline:
            candidates = powershell_find_image(POWERSHELL, INSTALL_APP)
            if candidates:
                candidate = candidates[0]
                candidate_pid = int(candidate["ProcessId"])
                try:
                    health_after = wait_health(candidate_pid, old_instance, timeout=4)
                    new_host_info = candidate
                    host_pid = candidate_pid
                    break
                except Exception:
                    time.sleep(0.25)
            else:
                time.sleep(0.25)
        if new_host_info is None:
            raise RuntimeError("安装状态为installed但helper没有启动托管应用")
        if not same_path(new_host_info.get("ExecutablePath", ""), INSTALL_APP):
            raise RuntimeError("重启宿主映像路径不匹配")
        host_handle, verified_host_info = open_expected_process(host_pid, INSTALL_APP, POWERSHELL)
        new_go_pid = int(health_after["pid"])
        go_handle, new_go_info = open_expected_process(new_go_pid, go_exe, POWERSHELL)
        if int(new_go_info["ParentProcessId"]) != host_pid:
            raise RuntimeError("更新后Go服务不是新桌面宿主的子进程")
        if health_after.get("instanceId") == old_instance:
            raise RuntimeError("更新后复用了旧Go运行身份")
        stored = request(f"/api/v1/scheduler/schedules/{schedule_id}")
        if stored.get("name") != "更新隔离持久化验证":
            raise RuntimeError("完整替换后数据库中的未来单次日程未保留")
        with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as connection:
            if connection.execute("PRAGMA user_version").fetchone()[0] != 2:
                raise RuntimeError("更新后SQLite schema版本变化")
            if connection.execute("SELECT name FROM schedules WHERE id=?", (schedule_id,)).fetchone() is None:
                raise RuntimeError("更新后用户数据库缺少原日程")
        if config.read_bytes() != config_bytes or data_unknown.read_bytes() != unknown_marker:
            raise RuntimeError("更新改写了配置或用户未知文件")
        if not install_unknown.is_file() or install_unknown.read_text(encoding="utf-8") != "不得被升级或恢复流程删除\n":
            raise RuntimeError("更新删除了manifest之外的用户文件")
        if not log_dir.is_dir():
            raise RuntimeError("更新破坏了用户日志目录")
        installed_files = check_manifest(install, manifest)
        if json.loads((install / "manifest.json").read_text(encoding="utf-8-sig"))["version"] != version:
            raise RuntimeError("安装目录manifest仍是旧版本")
        evidence["phases"].append({
            "phase": "installed-and-restarted",
            "status": installed,
            "hostPid": host_pid,
            "goPid": new_go_pid,
            "instanceId": health_after["instanceId"],
            "scheduleId": schedule_id,
            "manifestFilesVerified": installed_files,
            "logDirectoryPreserved": str(log_dir),
        })

        # 停止仅由本次smoke创建的新实例后恢复journal，确认原README/manifest复原。
        terminate_expected(host_handle, host_pid, INSTALL_APP, POWERSHELL)
        wait_handle(go_handle, 15, "更新后托管Go Job进程")
        KERNEL.CloseHandle(go_handle)
        go_handle = None
        KERNEL.CloseHandle(host_handle)
        host_handle = None
        journal = work / "journal.json"
        if not journal.is_file():
            raise RuntimeError("安装成功后没有事务journal，无法执行Recover验收")
        recover = subprocess.run(
            [
                POWERSHELL,
                "-NoProfile",
                "-NonInteractive",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
                str(helper),
                "-Mode",
                "Recover",
                "-JournalPath",
                str(journal),
            ],
            capture_output=True,
            text=True,
            timeout=180,
            creationflags=CREATE_NO_WINDOW,
        )
        if recover.returncode != 0:
            raise RuntimeError(f"Recover失败：{recover.stderr[-2000:]} {recover.stdout[-1000:]}")
        if readme.read_bytes() != old_readme or (install / "manifest.json").read_bytes() != old_manifest_bytes:
            raise RuntimeError("Recover未还原旧README和manifest")
        if config.read_bytes() != config_bytes or data_unknown.read_bytes() != unknown_marker:
            raise RuntimeError("Recover修改了配置或用户未知文件")
        if not install_unknown.is_file():
            raise RuntimeError("Recover删除了manifest之外的用户文件")
        with contextlib.closing(sqlite3.connect(f"file:{database.as_posix()}?mode=ro", uri=True)) as connection:
            if connection.execute("SELECT name FROM schedules WHERE id=?", (schedule_id,)).fetchone() is None:
                raise RuntimeError("Recover影响了用户数据库日程")
        evidence["phases"].append({"phase": "recover-complete", "journal": str(journal)})
        completed = True
    finally:
        # 错误路径只终止持有句柄且路径已核验的本smoke宿主/Go进程。
        if host_process is not None and host_handle is not None:
            try:
                terminate_expected(host_handle, host_pid, INSTALL_APP, POWERSHELL)
                host_process.wait(timeout=10)
            except Exception as error:
                evidence["cleanupError"] = str(error)
        elif host_handle is not None and host_pid is not None:
            try:
                terminate_expected(host_handle, host_pid, INSTALL_APP, POWERSHELL)
            except Exception as error:
                evidence["cleanupError"] = str(error)
        elif INSTALL_APP is not None:
            try:
                terminate_exact_image(INSTALL_APP, POWERSHELL)
            except Exception as error:
                evidence["cleanupError"] = str(error)
        if go_handle is not None:
            try:
                if host_pid is not None:
                    wait_handle(go_handle, 5, "托管Go Job进程")
                else:
                    raise RuntimeError("没有宿主PID可核验Go清理")
            except Exception as error:
                evidence["cleanupGoError"] = str(error)
                try:
                    go_info = powershell_info(POWERSHELL, go_pid)
                    if go_info and same_path(go_info.get("ExecutablePath", ""), go_exe):
                        terminate_expected(go_handle, go_pid, go_exe, POWERSHELL)
                except Exception as terminate_error:
                    evidence["cleanupGoTerminateError"] = str(terminate_error)
            KERNEL.CloseHandle(go_handle)
        elif go_exe is not None and host_pid is not None:
            try:
                # Go映像位于唯一新建的安装目录，不可能是用户手动服务路径。
                terminate_exact_image(go_exe, POWERSHELL)
            except Exception as error:
                evidence["cleanupGoError"] = str(error)
        if host_handle is not None:
            KERNEL.CloseHandle(host_handle)
        if helper_process is not None and helper_process.poll() is None:
            helper_process.terminate()
            try:
                helper_process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                evidence["cleanupHelperError"] = "隔离PowerShell助手未退出"
        if helper_output is not None:
            helper_output.close()
        host_log.close()
        evidence["result"] = "passed" if completed else "failed"
        evidence["helperLog"] = str(helper_log)
        evidence["hostLog"] = str(logs / "host.log")
    return root, evidence, completed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package", required=True, help="真实完整发行目录，例如TimeCue-0.1.0+1-windows-x64-时间戳")
    parser.add_argument("--updater", help="可选的install-update.ps1覆盖路径")
    args = parser.parse_args()
    if os.name != "nt":
        raise SystemExit("此真实应用更新smoke仅支持Windows x64")
    if platform.machine().lower() not in {"amd64", "x86_64"}:
        raise SystemExit(f"此smoke只支持Windows x64 Python，当前架构为{platform.machine()}")
    package = Path(args.package).resolve()
    evidence_stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    evidence_path = package.parent / f"{package.name}.update-smoke-evidence-{evidence_stamp}.json"
    root = None
    evidence = {"result": "failed", "package": str(package)}
    failure = None
    try:
        root, evidence, passed = run_smoke(package, args.updater)
        if not passed:
            failure = "更新隔离smoke未完成"
    except Exception as error:
        failure = str(error)
        if SMOKE_EVIDENCE is not None:
            evidence = SMOKE_EVIDENCE
        evidence["error"] = failure
    finally:
        if root is None:
            root = SMOKE_ROOT
        if root is not None:
            helper_log_path = evidence.get("helperLog") or str(
                root / "工作区" / "logs" / "updater.log"
            )
            host_log_path = evidence.get("hostLog") or str(
                root / "工作区" / "logs" / "host.log"
            )
            helper_tail = bounded_log_tail(helper_log_path)
            host_tail = bounded_log_tail(host_log_path)
            if helper_tail:
                evidence["helperLogTail"] = helper_tail
                evidence["helperLastError"] = helper_error_summary(helper_tail)
            if host_tail:
                evidence["hostLogTail"] = host_tail
            if evidence.get("cleanupError") or evidence.get("cleanupGoError") or evidence.get("cleanupHelperError"):
                evidence["temporaryDirectoryRemoved"] = False
                evidence["retainedTemporaryDirectory"] = str(root)
            else:
                try:
                    shutil.rmtree(root)
                    evidence["temporaryDirectoryRemoved"] = True
                except OSError as error:
                    evidence["temporaryDirectoryRemoved"] = False
                    evidence["retainedTemporaryDirectory"] = str(root)
                    evidence["cleanupError"] = str(error)
        try:
            evidence_path.write_text(
                json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8"
            )
        except OSError as error:
            raise SystemExit(f"无法写入smoke证据：{evidence_path}：{error}") from error
    if failure:
        helper_error = evidence.get("helperLastError")
        summary = f"；更新器最后错误：{helper_error}" if helper_error else ""
        raise SystemExit(f"FAIL 更新隔离smoke：{failure}{summary}\n证据：{evidence_path}")
    cleanup_errors = [
        evidence[key]
        for key in ("cleanupError", "cleanupGoError", "cleanupGoTerminateError", "cleanupHelperError")
        if evidence.get(key)
    ]
    if cleanup_errors:
        raise SystemExit(f"FAIL 更新smoke进程清理：{'；'.join(cleanup_errors)}\n证据：{evidence_path}")
    print("PASS 真实Windows更新smoke：Prepare校验、ready/commit、旧Go Job回收、安装重启、数据库保留及Recover")
    print("范围说明：同一发行构建模拟旧版本号；不验证通知回填，不声称跨正式Release端到端升级。")
    print(f"JSON证据：{evidence_path}")


if __name__ == "__main__":
    main()
