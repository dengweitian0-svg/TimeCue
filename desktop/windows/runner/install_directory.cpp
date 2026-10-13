#include "install_directory.h"

#include <shlobj.h>

#include <array>
#include <cstddef>
#include <cwchar>
#include <vector>

namespace {

constexpr wchar_t kInstallDirectoryEnvironmentVariable[] = L"TIMECUE_INSTALL_DIR";
constexpr wchar_t kUpdateScriptMarker[] = L"update.py";
constexpr wchar_t kManifestMarker[] = L"manifest.json";
constexpr wchar_t kCoreExecutableMarker[] = L"timecue-core.exe";

// 检查指定目录是否包含发行包的三个更新标记文件。
// directory：待检查的程序目录。
// 三个标记均为普通文件时返回 true，否则返回 false。
bool HasReleaseMarkers(const std::wstring& directory) {
    const std::array<const wchar_t*, 3> markers = {
        kUpdateScriptMarker,
        kManifestMarker,
        kCoreExecutableMarker,
    };
    for (const auto* marker : markers) {
        const auto path = directory + L"\\" + marker;
        const DWORD attributes = GetFileAttributesW(path.c_str());
        if (attributes == INVALID_FILE_ATTRIBUTES ||
            (attributes & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT)) != 0) {
            return false;
        }
    }
    return true;
}

// 把 Windows 错误码转换为便于诊断的中文错误信息。
// operation：发生错误的操作说明；error_code：Win32 错误码。
// 返回包含操作名称及系统消息的文本。
std::wstring MakeWindowsError(const wchar_t* operation, DWORD error_code) {
    wchar_t* system_message = nullptr;
    const DWORD length = FormatMessageW(
        FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM |
            FORMAT_MESSAGE_IGNORE_INSERTS,
        nullptr,
        error_code,
        MAKELANGID(LANG_NEUTRAL, SUBLANG_DEFAULT),
        reinterpret_cast<wchar_t*>(&system_message),
        0,
        nullptr);
    std::wstring message = operation;
    message += L"失败 (" + std::to_wstring(error_code) + L")";
    if (length != 0 && system_message != nullptr) {
        message += L"：";
        message.append(system_message, length);
        LocalFree(system_message);
    }
    return message;
}

// 从用户环境键读取安装目录变量。
// key：已打开的注册表键；value：接收现有值；error_status：接收 Win32 注册表错误码。
// 返回读取成功；变量不存在时也返回 true 并将 value 置空。
bool ReadInstallDirectory(HKEY key, std::wstring* value, LONG* error_status) {
    DWORD type = 0;
    DWORD byte_count = 0;
    LONG status = RegQueryValueExW(
        key, kInstallDirectoryEnvironmentVariable, nullptr, &type, nullptr, &byte_count);
    if (status == ERROR_FILE_NOT_FOUND) {
        value->clear();
        return true;
    }
    if (status != ERROR_SUCCESS || type != REG_SZ) {
        *error_status = status == ERROR_SUCCESS ? ERROR_INVALID_DATA : status;
        return false;
    }
    std::vector<wchar_t> buffer(byte_count / sizeof(wchar_t) + 1, L'\0');
    status = RegQueryValueExW(
        key,
        kInstallDirectoryEnvironmentVariable,
        nullptr,
        &type,
        reinterpret_cast<BYTE*>(buffer.data()),
        &byte_count);
    if (status != ERROR_SUCCESS) {
        *error_status = status;
        return false;
    }
    value->assign(buffer.data());
    return true;
}

// 更新当前进程中的安装目录环境变量。
// directory：需要设置的绝对程序目录。
// 设置成功返回 true，环境块分配失败返回 false。
bool SetProcessInstallDirectory(const std::wstring& directory) {
    return SetEnvironmentVariableW(kInstallDirectoryEnvironmentVariable, directory.c_str()) != 0;
}

}  // namespace

InstallDirectoryRegistrationResult RegisterInstallDirectory(
    const std::wstring& executable_path,
    const InstallDirectoryRegistrationOptions& options) {
    InstallDirectoryRegistrationResult result;
    const auto separator = executable_path.find_last_of(L"\\/");
    if (separator == std::wstring::npos || separator == 0) {
        result.error_message = L"无法从可执行文件路径确定 TimeCue 安装目录";
        return result;
    }
    const std::wstring directory = executable_path.substr(0, separator);
    if (!HasReleaseMarkers(directory)) {
        result.status = InstallDirectoryRegistrationStatus::not_release;
        return result;
    }

    HKEY key = nullptr;
    DWORD disposition = 0;
    const LONG open_status = RegCreateKeyExW(
        options.registry_root,
        options.registry_subkey.c_str(),
        0,
        nullptr,
        REG_OPTION_NON_VOLATILE,
        KEY_QUERY_VALUE | KEY_SET_VALUE,
        nullptr,
        &key,
        &disposition);
    if (open_status != ERROR_SUCCESS) {
        result.error_message = MakeWindowsError(L"打开用户环境注册表项", open_status);
        return result;
    }

    std::wstring existing_directory;
    LONG read_status = ERROR_SUCCESS;
    if (!ReadInstallDirectory(key, &existing_directory, &read_status)) {
        RegCloseKey(key);
        result.error_message = MakeWindowsError(L"读取安装目录环境变量", read_status);
        return result;
    }
    if (existing_directory == directory) {
        RegCloseKey(key);
        if (!SetProcessInstallDirectory(directory)) {
            result.error_message = MakeWindowsError(L"同步当前进程环境变量", GetLastError());
            return result;
        }
        result.status = InstallDirectoryRegistrationStatus::unchanged;
        return result;
    }

    const LONG write_status = RegSetValueExW(
        key,
        kInstallDirectoryEnvironmentVariable,
        0,
        REG_SZ,
        reinterpret_cast<const BYTE*>(directory.c_str()),
        static_cast<DWORD>((directory.size() + 1) * sizeof(wchar_t)));
    RegCloseKey(key);
    if (write_status != ERROR_SUCCESS) {
        result.error_message = MakeWindowsError(L"写入安装目录环境变量", write_status);
        return result;
    }
    if (!SetProcessInstallDirectory(directory)) {
        result.error_message = MakeWindowsError(L"同步当前进程环境变量", GetLastError());
        return result;
    }

    if (options.broadcast_change) {
        DWORD_PTR receiver_result = 0;
        if (!SendMessageTimeoutW(
                HWND_BROADCAST,
                WM_SETTINGCHANGE,
                0,
                reinterpret_cast<LPARAM>(L"Environment"),
                SMTO_ABORTIFHUNG | SMTO_BLOCK,
                3000,
                &receiver_result)) {
            const auto broadcast_error = MakeWindowsError(L"广播用户环境变更", GetLastError());
            OutputDebugStringW(broadcast_error.c_str());
        }
    }

    result.status = InstallDirectoryRegistrationStatus::registered;
    return result;
}

bool GetCurrentExecutablePath(std::wstring* executable_path, std::wstring* error_message) {
    std::vector<wchar_t> buffer(512);
    while (buffer.size() <= 32768) {
        SetLastError(ERROR_SUCCESS);
        const DWORD length = GetModuleFileNameW(
            nullptr, buffer.data(), static_cast<DWORD>(buffer.size()));
        if (length == 0) {
            *error_message = MakeWindowsError(L"读取 TimeCue 程序路径", GetLastError());
            return false;
        }
        if (length < buffer.size()) {
            const std::wstring module_path(buffer.data(), length);
            std::vector<wchar_t> absolute_buffer(512);
            while (absolute_buffer.size() <= 32768) {
                const DWORD absolute_length = GetFullPathNameW(
                    module_path.c_str(),
                    static_cast<DWORD>(absolute_buffer.size()),
                    absolute_buffer.data(),
                    nullptr);
                if (absolute_length == 0) {
                    *error_message = MakeWindowsError(L"解析 TimeCue 程序绝对路径", GetLastError());
                    return false;
                }
                if (absolute_length < absolute_buffer.size()) {
                    executable_path->assign(absolute_buffer.data(), absolute_length);
                    return true;
                }
                absolute_buffer.resize(static_cast<std::size_t>(absolute_length) + 1);
            }
            *error_message = L"TimeCue 程序路径超过 Windows 支持的长度";
            return false;
        }
        buffer.resize(buffer.size() * 2);
    }
    *error_message = L"TimeCue 程序路径超过 Windows 支持的长度";
    return false;
}
