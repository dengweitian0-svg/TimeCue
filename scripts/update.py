"""根据发行清单更新当前用户安装的 TimeCue。"""
import argparse
import ctypes
import hashlib
import json
import os
import re
import shutil
import stat
import sys
import tempfile
from pathlib import Path, PureWindowsPath

ENV_NAME = "TIMECUE_INSTALL_DIR"
REQUIRED_FILES = ("timecue_desktop.exe", "timecue-core.exe")
RESERVED_NAMES = {
    "CON", "PRN", "AUX", "NUL",
    *(f"COM{index}" for index in range(1, 10)),
    *(f"LPT{index}" for index in range(1, 10)),
}
ROOT_USER_CONFIGS = {
    "config.yaml", "config.yml", "config.json", "config.toml",
    "settings.yaml", "settings.yml", "settings.json", "settings.toml",
    "user_config.json", "user_settings.json",
}


class UpdateError(Exception):
    """可供终端用户理解的更新错误。"""


def _is_reparse(path):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400)


def _checked_path(root, relative, seen=None):
    if not isinstance(relative, str) or not relative or "\\" in relative:
        raise UpdateError(f"清单包含无效相对路径：{relative!r}")
    win_path = PureWindowsPath(relative)
    parts = relative.split("/")
    if win_path.is_absolute() or win_path.drive or any(part in ("", ".", "..") for part in parts):
        raise UpdateError(f"清单路径必须是安全的相对路径：{relative}")
    for part in parts:
        if ":" in part or part.endswith((".", " ")) or part.upper().split(".", 1)[0] in RESERVED_NAMES:
            raise UpdateError(f"清单路径包含 Windows 保留名称或流路径：{relative}")
        if part.casefold() in {"config", "configs", "settings", "logs", "log", "user-data", "userdata", "user-library"}:
            raise UpdateError(f"清单不得包含用户配置、数据或日志路径：{relative}")
    if len(parts) == 1 and parts[0].casefold() in ROOT_USER_CONFIGS:
        raise UpdateError(f"清单不得包含用户配置文件：{relative}")
    if any(parts[-1].casefold().endswith(suffix) for suffix in
           (".db", ".sqlite", ".sqlite3", ".log", ".db-wal", ".db-shm", ".db-journal")):
        raise UpdateError(f"清单不得包含用户数据库或日志文件：{relative}")
    key = relative.casefold()
    if seen is not None:
        if key in seen:
            raise UpdateError(f"清单路径大小写重复：{relative}")
        seen.add(key)
    path = root
    for part in parts:
        path = path / part
        if _is_reparse(path):
            raise UpdateError(f"拒绝经过符号链接或 reparse point：{path}")
    try:
        path.resolve(strict=False).relative_to(root.resolve())
    except ValueError as error:
        raise UpdateError(f"清单路径越界：{relative}") from error
    return path


def _read_manifest(root, verify=True, require_runtime=True):
    path = root / "manifest.json"
    if _is_reparse(path) or not path.is_file():
        raise UpdateError("发行目录缺少有效的 manifest.json")
    try:
        manifest = json.loads(path.read_text(encoding="utf-8-sig"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise UpdateError(f"无法读取发行清单：{error}") from error
    if not isinstance(manifest, dict) or manifest.get("platform") != "windows-x64":
        raise UpdateError("发行清单 platform 必须为 windows-x64")
    entries = manifest.get("files")
    if not isinstance(entries, list):
        raise UpdateError("发行清单 files 必须是数组")
    seen = set()
    files = {}
    for entry in entries:
        if not isinstance(entry, dict):
            raise UpdateError("发行清单文件项格式无效")
        relative = entry.get("path")
        path = _checked_path(root, relative, seen)
        digest = entry.get("sha256")
        if verify and (not isinstance(digest, str) or not re.fullmatch(r"[0-9a-fA-F]{64}", digest)):
            raise UpdateError(f"文件 SHA256 格式无效：{relative}")
        if relative.casefold() == "manifest.json":
            raise UpdateError("清单不得将 manifest.json 列为发行文件")
        if path.exists() and not path.is_file():
            raise UpdateError(f"清单路径不是普通文件：{relative}")
        if verify:
            if not path.is_file():
                raise UpdateError(f"发行文件不存在：{relative}")
            actual = _hash_file(path)
            if actual.casefold() != digest.casefold():
                raise UpdateError(f"发行文件校验失败：{relative}")
        files[relative] = path
    folded = {name.casefold() for name in files}
    if require_runtime and not all(name.casefold() in folded for name in REQUIRED_FILES):
        raise UpdateError("发行清单缺少 TimeCue 主程序或 Go 服务")
    return manifest, files


def _hash_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _inside_or_same(first, second):
    first_text = os.path.normcase(str(first)).casefold().rstrip("\\/")
    second_text = os.path.normcase(str(second)).casefold().rstrip("\\/")
    try:
        common = os.path.commonpath((first_text, second_text))
    except ValueError:
        return False
    return common == first_text or common == second_text


def _validate_target(target):
    if not target.is_absolute() or not target.is_dir():
        raise UpdateError(f"安装目录不存在或不是绝对路径：{target}")
    for name in REQUIRED_FILES:
        path = target / name
        if _is_reparse(path) or not path.is_file():
            raise UpdateError(f"目标不是有效的 TimeCue 安装目录，缺少 {name}")


def _windows_file_is_busy(path):
    if os.name != "nt":
        return False
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.CreateFileW.argtypes = (ctypes.c_wchar_p, ctypes.c_ulong, ctypes.c_ulong,
                                   ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_void_p)
    kernel.CreateFileW.restype = ctypes.c_void_p
    kernel.CloseHandle.argtypes = (ctypes.c_void_p,)
    handle = kernel.CreateFileW(str(path), 0x80000000, 0, None, 3, 0x80, None)
    invalid = ctypes.c_void_p(-1).value
    if handle == invalid:
        return True
    kernel.CloseHandle(handle)
    return False


def _persistent_install_path():
    if os.name == "nt":
        try:
            import winreg
            with winreg.OpenKey(winreg.HKEY_CURRENT_USER, "Environment") as key:
                value, _ = winreg.QueryValueEx(key, ENV_NAME)
                if isinstance(value, str) and value.strip():
                    return value
        except (OSError, ImportError):
            pass
    value = os.environ.get(ENV_NAME)
    return value if value and value.strip() else None


def _register_install(target):
    if os.name != "nt":
        raise UpdateError("安装路径登记仅支持 Windows")
    _validate_target(target)
    import winreg
    try:
        key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, "Environment", 0, winreg.KEY_SET_VALUE)
    except FileNotFoundError:
        key = winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, "Environment", 0, winreg.KEY_SET_VALUE)
    with key:
        winreg.SetValueEx(key, ENV_NAME, 0, winreg.REG_SZ, str(target))
    os.environ[ENV_NAME] = str(target)
    _broadcast_environment_change()


def _broadcast_environment_change():
    user32 = ctypes.WinDLL("user32", use_last_error=True)
    send = user32.SendMessageTimeoutW
    send.argtypes = (ctypes.c_void_p, ctypes.c_uint, ctypes.c_size_t, ctypes.c_wchar_p,
                     ctypes.c_uint, ctypes.c_uint, ctypes.POINTER(ctypes.c_size_t))
    send.restype = ctypes.c_void_p
    result = ctypes.c_size_t()
    send(0xFFFF, 0x001A, 0, "Environment", 0x0002, 5000, ctypes.byref(result))


def update(source, target):
    source = source.resolve(strict=True)
    target = target.resolve(strict=False)
    if _inside_or_same(source, target):
        raise UpdateError("发行目录与安装目录不能相同，也不能互相包含")
    target = target.resolve(strict=True)
    _validate_target(target)
    manifest, new_files = _read_manifest(source)
    for name in REQUIRED_FILES:
        if _windows_file_is_busy(target / name):
            raise UpdateError(f"检测到目标文件正在使用，已拒绝更新：{name}。请先退出 TimeCue")

    old_manifest_path = target / "manifest.json"
    old_files = {}
    if old_manifest_path.exists():
        old_manifest, old_files = _read_manifest(target, verify=False, require_runtime=False)
        del old_manifest
    else:
        old_files = {name: target / name for name in REQUIRED_FILES}

    old_keys = {name.casefold(): name for name in old_files}
    new_keys = {name.casefold(): name for name in new_files}
    # 不接管已有清单未拥有的文件，以免覆盖安装目录中的自有数据。
    if old_manifest_path.exists():
        for folded, name in new_keys.items():
            if folded not in old_keys:
                candidate = _checked_path(target, name)
                if candidate.exists():
                    raise UpdateError(f"发行文件与未登记的现有文件冲突：{name}")

    updates = list(new_files.items())
    # 用旧清单中的拼写定位大小写不敏感的现有文件。
    for index, (name, path) in enumerate(updates):
        prior = old_keys.get(name.casefold())
        if prior:
            updates[index] = (prior, path)
    removals = [name for folded, name in old_keys.items() if folded not in new_keys]

    transaction = Path(tempfile.mkdtemp(prefix=".timecue-update-", dir=target.parent))
    staged = transaction / "new"
    backups = transaction / "backup"
    staged.mkdir()
    backups.mkdir()
    changed = set()
    created_dirs = set()
    preserve_backup = False
    try:
        for relative, source_file in updates:
            stage_file = staged.joinpath(*relative.split("/"))
            stage_file.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source_file, stage_file)
        manifest_stage = staged / "manifest.json"
        shutil.copy2(source / "manifest.json", manifest_stage)
        staged_manifest, staged_files = _read_manifest(staged)
        del staged_manifest
        if set(name.casefold() for name in staged_files) != set(name.casefold() for name in new_files):
            raise UpdateError("发行清单在暂存期间发生变化")
        affected = {name for name, _ in updates} | set(removals) | {"manifest.json"}
        for relative in affected:
            destination = _checked_path(target, relative)
            if destination.exists():
                backup = backups.joinpath(*relative.split("/"))
                backup.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(destination, backup)

        try:
            for relative, _ in updates:
                destination = _checked_path(target, relative)
                parent = destination.parent
                missing = []
                while parent != target and not parent.exists():
                    missing.append(parent)
                    parent = parent.parent
                for directory in reversed(missing):
                    directory.mkdir()
                    created_dirs.add(directory)
                changed.add(relative)
                os.replace(staged.joinpath(*relative.split("/")), destination)
            for relative in removals:
                destination = _checked_path(target, relative)
                if destination.exists():
                    changed.add(relative)
                    destination.unlink()
            changed.add("manifest.json")
            os.replace(manifest_stage, old_manifest_path)
        except Exception as error:
            rollback_errors = []
            for relative in reversed(tuple(changed)):
                destination = target.joinpath(*relative.split("/"))
                backup = backups.joinpath(*relative.split("/"))
                try:
                    if backup.is_file():
                        destination.parent.mkdir(parents=True, exist_ok=True)
                        restore = transaction / "restore" / Path(*relative.split("/"))
                        restore.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copy2(backup, restore)
                        os.replace(restore, destination)
                    elif destination.exists():
                        destination.unlink()
                except Exception as rollback_error:
                    rollback_errors.append(f"{relative}: {rollback_error}")
            for directory in sorted(created_dirs, key=lambda item: len(item.parts), reverse=True):
                try:
                    directory.rmdir()
                except OSError:
                    pass
            if rollback_errors:
                preserve_backup = True
                raise UpdateError(f"更新失败：{error}；回滚也失败：{'；'.join(rollback_errors)}。备份保留在：{transaction}") from error
            raise UpdateError(f"更新失败，已恢复原文件：{error}") from error
    finally:
        if not preserve_backup:
            shutil.rmtree(transaction, ignore_errors=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--register", metavar="旧安装目录", help="登记当前用户的旧安装目录")
    options = parser.parse_args(argv)
    try:
        if options.register:
            target = Path(options.register)
            if not target.is_absolute():
                raise UpdateError("--register 必须提供绝对安装路径")
            _register_install(target.resolve())
            print(f"已登记安装目录：{target.resolve()}")
            return 0
        value = _persistent_install_path()
        if not value:
            raise UpdateError(f"未找到 {ENV_NAME}。请在新解压的发行目录运行 python update.py --register <旧安装绝对路径>，然后再运行 python update.py")
        target = Path(value)
        if not target.is_absolute():
            raise UpdateError(f"{ENV_NAME} 必须是绝对路径：{value}")
        update(Path(__file__).resolve().parent, target)
        print(f"TimeCue 已更新：{target}")
        return 0
    except (UpdateError, OSError) as error:
        print(f"TimeCue 更新失败：{error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
