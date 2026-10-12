#include "launch_configuration.h"
#include <windows.h>
#include <shlobj.h>
#include <filesystem>

std::wstring LaunchWide(const std::string& value) {
    const int size = MultiByteToWideChar(
        CP_UTF8, MB_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), nullptr, 0);
    if (!size) return L"";
    std::wstring result(size, L'\0');
    MultiByteToWideChar(
        CP_UTF8,
        MB_ERR_INVALID_CHARS,
        value.data(),
        static_cast<int>(value.size()),
        result.data(),
        size);
    return result;
}

namespace {
// 将规范化后的 Windows 宽字符串转回 UTF-8。
// value：待转换的 UTF-16 文本。
// 返回 UTF-8 字符串；空输入或转换失败返回空串。
std::string Utf8(const std::wstring& value) {
    const int size = WideCharToMultiByte(
        CP_UTF8,
        WC_ERR_INVALID_CHARS,
        value.data(),
        static_cast<int>(value.size()),
        nullptr,
        0,
        nullptr,
        nullptr);
    if (!size) return "";
    std::string result(size, '\0');
    WideCharToMultiByte(
        CP_UTF8,
        WC_ERR_INVALID_CHARS,
        value.data(),
        static_cast<int>(value.size()),
        result.data(),
        size,
        nullptr,
        nullptr);
    return result;
}

// 将启动路径转换为规范化的绝对路径，并去掉根目录以外的末尾分隔符。
// value：UTF-8 路径
// require_absolute：true 时拒绝相对路径。
// 返回 UTF-8 绝对路径。如果存在空路径、转换失败或无效路径，则返回空串，不检查文件是否存在。
std::string Absolute(const std::string& value, bool require_absolute) {
    const auto wide = LaunchWide(value);
    if (wide.empty() || (require_absolute && !std::filesystem::path(wide).is_absolute())) return "";
    DWORD size = GetFullPathNameW(wide.c_str(), 0, nullptr, nullptr);
    if (!size || size > 32768) return "";
    std::wstring full(size, L'\0');
    const DWORD count = GetFullPathNameW(wide.c_str(), size, full.data(), nullptr);
    if (!count || count >= size) return "";
    full.resize(count);
    const auto normalized = std::filesystem::path(full).lexically_normal();
    full = normalized.wstring();
    const auto root_size = normalized.root_path().wstring().size();
    while (full.size() > root_size && (full.back() == L'\\' || full.back() == L'/'))
        full.pop_back();
    return Utf8(full);
}

// 按 Windows 忽略大小写规则比较两条已规范化路径。
// a、b：待比较的 UTF-8 路径。
// 返回 true 表示相同。
bool SamePath(const std::string& a, const std::string& b) {
    const auto left = LaunchWide(a), right = LaunchWide(b);
    return CompareStringOrdinal(left.c_str(), static_cast<int>(left.size()), right.c_str(), static_cast<int>(right.size()), TRUE) == CSTR_EQUAL;
}
}  // 匿名命名空间结束

std::optional<LaunchConfiguration> ParseLaunchConfiguration(
    const std::vector<std::string>& arguments) {
    LaunchConfiguration result;
    for (const auto& argument : arguments) {
        if (argument.rfind("--backend=", 0) == 0) {
            if (result.backend_explicit) return std::nullopt;
            result.backend_explicit = true;
            result.backend = argument.substr(10);
            if (result.backend != "managed" && result.backend != "external") return std::nullopt;
        } else if (argument.rfind("--data-dir=", 0) == 0) {
            if (result.data_explicit) return std::nullopt;
            result.data_explicit = true;
            result.data_dir = Absolute(argument.substr(11), false);
            if (result.data_dir.empty()) return std::nullopt;
        } else if (argument.rfind("--go-executable=", 0) == 0) {
            if (result.executable_explicit) return std::nullopt;
            result.executable_explicit = true;
            result.executable = Absolute(argument.substr(16), true);
            if (result.executable.empty()) return std::nullopt;
        } else if (argument != "--login-start" && argument != "--start-hidden" &&
            argument.rfind("--activation-payload=", 0) != 0) {
            return std::nullopt;
        }
    }
    // 完整性在首次 Dart 装配核验；转交可以只声明一个待核对的选项。
    return result;
}

bool CompatibleLaunchConfiguration(
    const LaunchConfiguration& current, const LaunchConfiguration& requested) {
    return (!requested.backend_explicit || requested.backend == current.backend) &&
        (!requested.data_explicit || SamePath(requested.data_dir, current.data_dir)) &&
        (!requested.executable_explicit || SamePath(requested.executable, current.executable));
}

bool ResolveLaunchDefaults(LaunchConfiguration& configuration) {
    if (!configuration.backend_explicit) configuration.backend = "managed";
    if (configuration.backend == "external") return true;
    if (!configuration.data_explicit) {
        PWSTR directory = nullptr;
        const HRESULT result = SHGetKnownFolderPath(FOLDERID_LocalAppData, 0, nullptr, &directory);
        if (FAILED(result)) return false;
        configuration.data_dir = Utf8((std::filesystem::path(directory) / L"TimeCue").wstring());
        CoTaskMemFree(directory);
        if (configuration.data_dir.empty()) return false;
    }
    if (!configuration.executable_explicit) {
        std::wstring executable(32768, L'\0');
        const DWORD count = GetModuleFileNameW(nullptr, executable.data(), static_cast<DWORD>(executable.size()));
        if (!count || count >= executable.size()) return false;
        executable.resize(count);
        configuration.executable = Utf8((std::filesystem::path(executable).parent_path() / L"timecue-core.exe").wstring());
        if (configuration.executable.empty()) return false;
    }
    return true;
}
