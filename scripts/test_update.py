"""TimeCue Python 更新器的隔离测试。"""
import hashlib
import json
import os
import sys
import tempfile
import types
import unittest
import ctypes
from pathlib import Path
from unittest import mock

import update


class FakeRegistry:
    HKEY_CURRENT_USER = object()
    KEY_SET_VALUE = 2
    REG_SZ = 1
    REG_EXPAND_SZ = 2

    def __init__(self, value=None, exists=True):
        self.value = value
        self.exists = exists

    def OpenKey(self, *args):
        if not self.exists:
            raise FileNotFoundError("missing key")
        return self

    def CreateKeyEx(self, *args):
        self.exists = True
        return self

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def QueryValueEx(self, key, name):
        if self.value is None:
            raise OSError("missing")
        return self.value, self.REG_EXPAND_SZ

    def SetValueEx(self, key, name, reserved, kind, value):
        self.value = value


class UpdateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="TimeCue 中文 ")
        self.root = Path(self.temp.name).resolve()
        self.source = self.root / "新发行目录"
        self.target = self.root / "旧安装目录"
        self.source.mkdir()
        self.target.mkdir()
        self.payload = {
            "timecue_desktop.exe": b"new desktop",
            "timecue-core.exe": b"new core",
            "data/app.so": b"new flutter",
        }
        self.write_release(self.source, self.payload)
        (self.target / "timecue_desktop.exe").write_bytes(b"old desktop")
        (self.target / "timecue-core.exe").write_bytes(b"old core")

    def tearDown(self):
        self.temp.cleanup()

    @staticmethod
    def write_release(root, files):
        entries = []
        for name, content in files.items():
            path = root.joinpath(*name.split("/"))
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
            entries.append({"path": name, "sha256": hashlib.sha256(content).hexdigest()})
        (root / "manifest.json").write_text(
            json.dumps({"platform": "windows-x64", "files": entries}), encoding="utf-8"
        )

    def old_manifest(self, files):
        self.write_release(self.target, files)

    def test_success_cleans_owned_stale_files_and_keeps_extras(self):
        old = {
            "timecue_desktop.exe": b"old desktop",
            "timecue-core.exe": b"old core",
            "data/app.so": b"old Flutter",
        }
        old["stale.dll"] = b"old owned file"
        self.old_manifest(old)
        (self.target / "stale.dll").write_bytes(b"old owned file")
        (self.target / "manual.txt").write_text("keep")
        (self.target / "user-data.db").write_bytes(b"keep data")
        update.update(self.source, self.target)
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"new desktop")
        self.assertEqual((self.target / "data/app.so").read_bytes(), b"new flutter")
        self.assertFalse((self.target / "stale.dll").exists())
        self.assertEqual((self.target / "manual.txt").read_text(), "keep")
        self.assertEqual((self.target / "user-data.db").read_bytes(), b"keep data")
        self.assertTrue((self.target / "manifest.json").is_file())

    def test_old_install_without_manifest_overlays_new_files(self):
        (self.target / "data").mkdir()
        (self.target / "data/app.so").write_bytes(b"old flutter")
        update.update(self.source, self.target)
        self.assertEqual((self.target / "data/app.so").read_bytes(), b"new flutter")

    def test_missing_environment_and_bad_target(self):
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(update, "_persistent_install_path", return_value=None):
            self.assertEqual(update.main([]), 1)
        with self.assertRaises(update.UpdateError):
            update._validate_target(self.root / "not install")

    def test_same_or_nested_source_target_rejected(self):
        with self.assertRaises(update.UpdateError):
            update.update(self.source, self.source)
        nested = self.target / "nested"
        nested.mkdir()
        with self.assertRaises(update.UpdateError):
            update.update(nested, self.target)
        with self.assertRaises(update.UpdateError):
            update.update(self.source, self.source / "installation")

    def test_windows_root_and_unc_containment_uses_windows_path_rules(self):
        self.assertTrue(update._inside_or_same(r"D:\source", "D:\\"))
        self.assertTrue(update._inside_or_same("D:\\", r"D:\source"))
        self.assertTrue(update._inside_or_same(r"d:\SOURCE", "D:\\"))
        self.assertFalse(update._inside_or_same(r"C:\source", "D:\\"))
        self.assertTrue(update._inside_or_same(r"\\server\share\source", "\\\\server\\share\\"))
        self.assertTrue(update._inside_or_same("\\\\server\\share\\", r"\\server\share\source"))
        self.assertFalse(update._inside_or_same(r"\\server\share-one\source", "\\\\server\\share-two\\"))
        self.assertTrue(update._inside_or_same(r"D:\one\child", r"D:\one"))
        self.assertTrue(update._inside_or_same(r"D:\one", r"D:\one\child"))
        self.assertFalse(update._inside_or_same(r"D:\one", r"D:\two"))

    def test_hash_and_path_attacks_rejected(self):
        manifest = json.loads((self.source / "manifest.json").read_text())
        manifest["files"][0]["sha256"] = "0" * 64
        (self.source / "manifest.json").write_text(json.dumps(manifest))
        with self.assertRaisesRegex(update.UpdateError, "校验失败"):
            update._read_manifest(self.source)
        for value in ("../escape", "C:/escape", "folder/file:stream", "CON.txt", "folder//file"):
            with self.subTest(value=value), self.assertRaises(update.UpdateError):
                update._checked_path(self.source, value)
        with self.assertRaises(update.UpdateError):
            update._checked_path(self.source, "A.txt", {"a.txt"})

    def test_collision_with_unowned_existing_file_is_preserved(self):
        self.old_manifest({"timecue_desktop.exe": b"old desktop", "timecue-core.exe": b"old core"})
        (self.target / "data").mkdir()
        (self.target / "data/app.so").write_bytes(b"manual file")
        with self.assertRaisesRegex(update.UpdateError, "冲突"):
            update.update(self.source, self.target)
        self.assertEqual((self.target / "data/app.so").read_bytes(), b"manual file")

    def test_replace_failure_rolls_back_already_replaced_files(self):
        original = update.os.replace
        count = 0

        def fail_second(source, destination):
            nonlocal count
            if Path(destination).parent == self.target:
                count += 1
                if count == 2:
                    raise OSError("injected replace error")
            return original(source, destination)

        with mock.patch.object(update.os, "replace", side_effect=fail_second):
            with self.assertRaisesRegex(update.UpdateError, "已恢复"):
                update.update(self.source, self.target)
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"old desktop")
        self.assertEqual((self.target / "timecue-core.exe").read_bytes(), b"old core")
        self.assertFalse((self.target / "manifest.json").exists())

    def test_failed_manifest_commit_restores_old_manifest_and_removed_file(self):
        old = {
            "timecue_desktop.exe": b"old desktop",
            "timecue-core.exe": b"old core",
            "data/app.so": b"old Flutter",
        }
        old["stale.dll"] = b"old stale"
        self.old_manifest(old)
        stale = self.target / "stale.dll"
        stale.write_bytes(b"old stale")
        old_manifest = (self.target / "manifest.json").read_bytes()
        original = update.os.replace

        def fail_manifest(source, destination):
            if (Path(destination) == self.target / "manifest.json" and Path(source).name == "manifest.json"
                    and Path(source).parent.name == "new"):
                raise OSError("injected manifest commit error")
            return original(source, destination)

        with mock.patch.object(update.os, "replace", side_effect=fail_manifest):
            with self.assertRaisesRegex(update.UpdateError, "已恢复"):
                update.update(self.source, self.target)
        self.assertEqual((self.target / "manifest.json").read_bytes(), old_manifest)
        self.assertEqual(stale.read_bytes(), b"old stale")
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"old desktop")

    def test_rollback_failure_keeps_backup_and_reports_location(self):
        original = update.os.replace
        transaction = None

        def fail_commit_and_rollback(source, destination):
            nonlocal transaction
            source_path = Path(source)
            destination_path = Path(destination)
            if ".timecue-update-" in str(source_path):
                transaction = next(parent for parent in source_path.parents if parent.name.startswith(".timecue-update-"))
            if destination_path == self.target / "timecue-core.exe" and source_path.name == "timecue-core.exe" and "new" in source_path.parts:
                raise OSError("injected install error")
            if destination_path == self.target / "timecue_desktop.exe" and "restore" in source_path.parts:
                raise OSError("injected rollback error")
            return original(source, destination)

        with mock.patch.object(update.os, "replace", side_effect=fail_commit_and_rollback):
            with self.assertRaisesRegex(update.UpdateError, "回滚也失败") as caught:
                update.update(self.source, self.target)
        backup_path = Path(str(caught.exception).split("备份保留在：", 1)[1])
        self.assertTrue(backup_path.is_dir())
        self.assertEqual((backup_path / "backup/timecue_desktop.exe").read_bytes(), b"old desktop")
        if transaction:
            self.assertEqual(backup_path, transaction)
            update.shutil.rmtree(backup_path)

    def test_corrupted_staged_copy_fails_before_install_mutation(self):
        original = update.shutil.copy2

        def corrupt_stage(source, destination, *args, **kwargs):
            result = original(source, destination, *args, **kwargs)
            destination = Path(destination)
            if destination.name == "timecue_desktop.exe" and ".timecue-update-" in str(destination):
                destination.write_bytes(b"corrupted staged copy")
            return result

        with mock.patch.object(update.shutil, "copy2", side_effect=corrupt_stage):
            with self.assertRaisesRegex(update.UpdateError, "校验失败"):
                update.update(self.source, self.target)
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"old desktop")
        self.assertEqual((self.target / "timecue-core.exe").read_bytes(), b"old core")

    def test_copy_failure_happens_before_mutation(self):
        with mock.patch.object(update.shutil, "copy2", side_effect=OSError("injected copy error")):
            with self.assertRaises(OSError):
                update.update(self.source, self.target)
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"old desktop")

    def test_busy_executable_refuses_update(self):
        with mock.patch.object(update, "_windows_file_is_busy", side_effect=lambda path: path.name == "timecue_desktop.exe"):
            with self.assertRaisesRegex(update.UpdateError, "正在使用"):
                update.update(self.source, self.target)

    @unittest.skipUnless(os.name == "nt", "Windows 文件共享锁检测")
    def test_windows_no_share_handle_refuses_update_without_mutation(self):
        old_files = {
            "timecue_desktop.exe": b"old desktop",
            "timecue-core.exe": b"old core",
            "data/app.so": b"old Flutter",
        }
        self.old_manifest(old_files)
        (self.target / "timecue_desktop.exe").write_bytes(b"old desktop")
        (self.target / "timecue-core.exe").write_bytes(b"old core")
        (self.target / "data/app.so").write_bytes(b"old Flutter")
        old_manifest = (self.target / "manifest.json").read_bytes()
        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        kernel.CreateFileW.argtypes = (ctypes.c_wchar_p, ctypes.c_ulong, ctypes.c_ulong,
                                       ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_void_p)
        kernel.CreateFileW.restype = ctypes.c_void_p
        kernel.CloseHandle.argtypes = (ctypes.c_void_p,)
        handle = kernel.CreateFileW(str(self.target / "timecue_desktop.exe"), 0x80000000, 0,
                                    None, 3, 0x80, None)
        self.assertNotEqual(handle, ctypes.c_void_p(-1).value)
        try:
            with self.assertRaisesRegex(update.UpdateError, "正在使用"):
                update.update(self.source, self.target)
        finally:
            kernel.CloseHandle(handle)
        self.assertEqual((self.target / "timecue_desktop.exe").read_bytes(), b"old desktop")
        self.assertEqual((self.target / "manifest.json").read_bytes(), old_manifest)

    @unittest.skipUnless(os.name == "nt", "Windows 已加载映像写权限检测")
    def test_windows_loaded_python_executable_is_detected_as_busy(self):
        self.assertTrue(update._windows_file_is_busy(Path(sys.executable)))

    def test_registry_value_wins_over_stale_process_value(self):
        registry = FakeRegistry(str(self.target))
        module = types.ModuleType("winreg")
        module.__dict__.update({key: value for key, value in vars(FakeRegistry).items() if not key.startswith("__")})
        module.OpenKey = registry.OpenKey
        module.QueryValueEx = registry.QueryValueEx
        with mock.patch.object(update.os, "name", "nt"), mock.patch.dict(sys.modules, {"winreg": module}), \
                mock.patch.dict(os.environ, {update.ENV_NAME: str(self.root / "stale")}, clear=False):
            self.assertEqual(update._persistent_install_path(), str(self.target))

    def test_register_sets_user_registry_and_process_value(self):
        registry = FakeRegistry(exists=False)
        module = types.ModuleType("winreg")
        module.HKEY_CURRENT_USER = registry.HKEY_CURRENT_USER
        module.KEY_SET_VALUE = registry.KEY_SET_VALUE
        module.REG_EXPAND_SZ = registry.REG_EXPAND_SZ
        module.REG_SZ = registry.REG_SZ
        module.OpenKey = registry.OpenKey
        module.CreateKeyEx = registry.CreateKeyEx
        module.SetValueEx = registry.SetValueEx
        with mock.patch.object(update.os, "name", "nt"), mock.patch.dict(sys.modules, {"winreg": module}), \
                mock.patch.object(update, "_broadcast_environment_change"), \
                mock.patch.dict(os.environ, {}, clear=True):
            update._register_install(self.target)
            self.assertEqual(registry.value, str(self.target))
            self.assertEqual(os.environ[update.ENV_NAME], str(self.target))

    def test_stage_is_created_on_install_volume(self):
        real_mkdtemp = update.tempfile.mkdtemp
        calls = []

        def record_stage(*args, **kwargs):
            calls.append(kwargs.get("dir"))
            return real_mkdtemp(*args, **kwargs)

        with mock.patch.object(update.tempfile, "mkdtemp", side_effect=record_stage):
            update.update(self.source, self.target)
        self.assertEqual(Path(calls[0]), self.target.parent)

    def test_user_config_and_database_sidecars_cannot_be_listed(self):
        for value in ("config.yaml", "settings.json", "data/session.db-wal", "data/session.db-shm"):
            with self.subTest(value=value), self.assertRaises(update.UpdateError):
                update._checked_path(self.source, value)
        self.assertEqual(update._checked_path(self.source, "configuration.dll").name, "configuration.dll")


if __name__ == "__main__":
    unittest.main()
