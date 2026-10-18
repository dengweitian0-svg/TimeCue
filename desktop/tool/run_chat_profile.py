"""运行已构建的 Windows profile 聊天验收程序，只清理本 runner 自有进程。"""
from __future__ import annotations

import argparse
import ctypes
import json
import os
import subprocess
import sys
import time
from pathlib import Path


def main() -> int:
    root = Path(__file__).resolve().parents[2]
    default_app = root / "desktop/build/windows/x64/runner/Profile/timecue_desktop.exe"
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, default=default_app)
    parser.add_argument("--timeout", type=float, default=240)
    options = parser.parse_args()
    app = options.app.resolve()
    report_path = root / "desktop/build/chat-profile/profile.json"
    log_path = root / "desktop/build/chat-profile/flutter-output.txt"
    report_path.parent.mkdir(parents=True, exist_ok=True)
    if not app.is_file():
        print(json.dumps({"passed": False, "error": f"找不到已构建的 profile 程序：{app}"}, ensure_ascii=False))
        return 2
    if os.name == "nt":
        user32 = ctypes.WinDLL("user32", use_last_error=True)
        user32.FindWindowW.argtypes = [ctypes.c_wchar_p, ctypes.c_wchar_p]
        user32.FindWindowW.restype = ctypes.c_void_p
        existing = user32.FindWindowW(None, "TimeCue")
        if existing:
            result = {
                "passed": False,
                "error": "检测到现有 TimeCue 顶层窗口；为避免触碰已运行实例，已拒绝启动 profile smoke。",
                "existing_window_detected": True,
                "database_touched": False,
            }
            report_path.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
            print(json.dumps(result, ensure_ascii=False, indent=2))
            return 1
    report_path.unlink(missing_ok=True)
    environment = os.environ.copy()
    environment["TIMECUE_CHAT_PROFILE_OUTPUT"] = str(report_path)
    started = time.monotonic()
    process: subprocess.Popen[bytes] | None = None
    try:
        with log_path.open("wb") as output:
            process = subprocess.Popen(
                [str(app)],
                cwd=root / "desktop",
                env=environment,
                stdout=output,
                stderr=subprocess.STDOUT,
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)
                | getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
            try:
                return_code = process.wait(timeout=options.timeout)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
                print(json.dumps({"passed": False, "error": f"profile smoke 超时（{options.timeout:g}s）", "pid": process.pid}, ensure_ascii=False))
                return 1
        if not report_path.is_file():
            print(json.dumps({
                "passed": False,
                "error": "profile 程序未写出结果 JSON",
                "exit_code": return_code,
                "log": str(log_path),
            }, ensure_ascii=False))
            return 1
        result = json.loads(report_path.read_text(encoding="utf-8"))
        if result.get("schema_version") != 4 or result.get("test_id") != "timecue-chat-profile-v1":
            result["passed"] = False
            result.setdefault("failures", []).append("profile 报告 schema/test_id 不匹配，smoke 可能没有运行")
        if result.get("environment", {}).get("profile_mode") is not True:
            result["passed"] = False
            result.setdefault("failures", []).append("目标程序不是 Flutter profile 模式")
        result["runner"] = {
            "exit_code": return_code,
            "elapsed_ms": round((time.monotonic() - started) * 1000),
            "pid": process.pid,
            "log": str(log_path),
            "profile_app": str(app),
            "existing_window_preflight_passed": True,
            "business_database_or_user_data_touched": False,
        }
        report_path.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        performance = result.get("performance", {})
        if performance.get("frame_target_met") is False:
            print(
                "FRAME TARGET NOT MET (diagnostic only; it does not fail functional exit status).",
                flush=True,
            )
        summary = {
            "passed": result.get("passed"),
            "frame_target_met": performance.get("frame_target_met"),
            "failures": result.get("failures", []),
            "target_findings": performance.get("target_findings", []),
            "performance": {
                "cold_start": performance.get("cold_start"),
                "cases": {
                    name: {
                        "frame_count": case.get("frame_count"),
                        "build_p50_us": case.get("build_us", {}).get("p50"),
                        "build_p95_us": case.get("build_us", {}).get("p95"),
                        "build_max_us": case.get("build_us", {}).get("max"),
                        "raster_p50_us": case.get("raster_us", {}).get("p50"),
                        "raster_p95_us": case.get("raster_us", {}).get("p95"),
                        "raster_max_us": case.get("raster_us", {}).get("max"),
                    }
                    for name, case in performance.get("cases", {}).items()
                },
            },
            "report": str(report_path),
            "log": str(log_path),
            "runner": result.get("runner"),
        }
        print(json.dumps(summary, ensure_ascii=False, indent=2), flush=True)
        return 0 if return_code == 0 and result.get("passed") is True else 1
    except (OSError, json.JSONDecodeError) as error:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        print(json.dumps({"passed": False, "error": str(error)}, ensure_ascii=False))
        return 1


if __name__ == "__main__":
    sys.exit(main())
