#include "install_directory.h"

#include <windows.h>

#include <filesystem>
#include <fstream>
#include <iostream>
#include <iterator>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

// 在条件不满足时终止当前测试。
// condition：需要成立的条件；message：失败说明。
// 条件成立时正常返回，失败时抛出运行时异常。
void Check(bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

// 生成本次进程专用的隔离注册表子项名称。
// 无参数。
// 返回不会与其他测试进程冲突的 HKCU 子项路径。
std::wstring MakeRegistrySubkey() {
    return L"Software\\TimeCueNativeTests\\InstallDirectory\\" +
        std::to_wstring(GetCurrentProcessId());
}

// 创建包含发行包标记的测试目录。
// directory：需要准备标记文件的程序目录。
// 返回该目录中模拟的可执行文件完整路径。
std::wstring CreateReleaseDirectory(const std::filesystem::path& directory) {
    std::filesystem::create_directories(directory);
    for (const auto* marker : {L"update.py", L"manifest.json", L"timecue-core.exe"}) {
        std::ofstream file(directory / marker, std::ios::binary);
        Check(file.is_open(), "create release marker");
        file << "test";
    }
    return (directory / L"TimeCue.exe").wstring();
}

// 从隔离注册表键读取安装目录并确认类型为 REG_SZ。
// subkey：当前测试持有的 HKCU 注册表子项路径。
// 返回登记的安装目录文本。
std::wstring ReadRegisteredPath(const std::wstring& subkey) {
    HKEY key = nullptr;
    Check(RegOpenKeyExW(HKEY_CURRENT_USER, subkey.c_str(), 0, KEY_QUERY_VALUE, &key) ==
        ERROR_SUCCESS, "open isolated registry key");
    DWORD type = 0;
    DWORD byte_count = 0;
    Check(RegQueryValueExW(key, L"TIMECUE_INSTALL_DIR", nullptr, &type, nullptr, &byte_count) ==
        ERROR_SUCCESS, "query registered value size");
    Check(type == REG_SZ, "registered value must be REG_SZ");
    std::vector<wchar_t> buffer(byte_count / sizeof(wchar_t) + 1, L'\0');
    Check(RegQueryValueExW(key, L"TIMECUE_INSTALL_DIR", nullptr, &type,
        reinterpret_cast<BYTE*>(buffer.data()), &byte_count) == ERROR_SUCCESS,
        "read registered value");
    RegCloseKey(key);
    return buffer.data();
}

// 删除本测试创建的隔离注册表子项。
// subkey：当前测试创建的 HKCU 子项路径。
// 删除成功或子项已不存在时返回 true。
bool RemoveRegistryKey(const std::wstring& subkey) {
    const LONG status = RegDeleteTreeW(HKEY_CURRENT_USER, subkey.c_str());
    return status == ERROR_SUCCESS || status == ERROR_FILE_NOT_FOUND;
}

// 验证正式发行目录的登记、值变化及不变化行为。
// root：测试用临时根目录；subkey：隔离注册表子项。
// 全部断言通过时正常返回。
void TestRegistration(const std::filesystem::path& root, const std::wstring& subkey) {
    InstallDirectoryRegistrationOptions options;
    options.registry_root = HKEY_CURRENT_USER;
    options.registry_subkey = subkey;
    options.broadcast_change = false;

    const auto first_directory = root / L"安装 路径 一";
    const auto first_executable = CreateReleaseDirectory(first_directory);
    auto result = RegisterInstallDirectory(first_executable, options);
    Check(result.status == InstallDirectoryRegistrationStatus::registered,
        "first release directory should be registered");
    Check(ReadRegisteredPath(subkey) == first_directory.wstring(), "Unicode path registration");
    wchar_t environment_value[32768]{};
    const DWORD environment_length = GetEnvironmentVariableW(
        L"TIMECUE_INSTALL_DIR", environment_value, static_cast<DWORD>(std::size(environment_value)));
    Check(environment_length != 0 && std::wstring(environment_value) == first_directory.wstring(),
        "process environment should be synchronized");

    result = RegisterInstallDirectory(first_executable, options);
    Check(result.status == InstallDirectoryRegistrationStatus::unchanged,
        "same directory should not rewrite registration");

    const auto second_directory = root / L"更新 后目录";
    const auto second_executable = CreateReleaseDirectory(second_directory);
    result = RegisterInstallDirectory(second_executable, options);
    Check(result.status == InstallDirectoryRegistrationStatus::registered,
        "changed release directory should be registered");
    Check(ReadRegisteredPath(subkey) == second_directory.wstring(), "updated path registration");
}

// 验证缺失任一发行标记时不会创建注册表值。
// root：测试用临时根目录；subkey：隔离注册表子项。
// 断言通过时正常返回。
void TestNonRelease(const std::filesystem::path& root, const std::wstring& subkey) {
    const auto development_directory = root / L"development build";
    std::filesystem::create_directories(development_directory);
    const auto executable = development_directory / L"TimeCue.exe";
    std::ofstream file(executable, std::ios::binary);
    file << "test";
    file.close();

    InstallDirectoryRegistrationOptions options;
    options.registry_root = HKEY_CURRENT_USER;
    options.registry_subkey = subkey;
    options.broadcast_change = false;
    const auto result = RegisterInstallDirectory(executable.wstring(), options);
    Check(result.status == InstallDirectoryRegistrationStatus::not_release,
        "development directory should be ignored");
    HKEY key = nullptr;
    Check(RegOpenKeyExW(HKEY_CURRENT_USER, subkey.c_str(), 0, KEY_QUERY_VALUE, &key) ==
        ERROR_FILE_NOT_FOUND, "non-release directory must not create registry key");

    const auto partial_directory = root / L"partial release markers";
    std::filesystem::create_directories(partial_directory);
    std::ofstream partial_marker(partial_directory / L"update.py", std::ios::binary);
    partial_marker << "test";
    partial_marker.close();
    const auto partial_result = RegisterInstallDirectory(
        (partial_directory / L"TimeCue.exe").wstring(), options);
    Check(partial_result.status == InstallDirectoryRegistrationStatus::not_release,
        "partial release markers should be ignored");
    Check(RegOpenKeyExW(HKEY_CURRENT_USER, subkey.c_str(), 0, KEY_QUERY_VALUE, &key) ==
        ERROR_FILE_NOT_FOUND, "partial markers must not create registry key");
}

// 验证无效注册表根句柄时返回可见失败结果。
// executable：发行目录中的模拟程序路径；subkey：隔离注册表子项。
// 断言通过时正常返回。
void TestFailure(const std::wstring& executable, const std::wstring& subkey) {
    InstallDirectoryRegistrationOptions options;
    options.registry_root = nullptr;
    options.registry_subkey = subkey;
    options.broadcast_change = false;
    const auto result = RegisterInstallDirectory(executable, options);
    Check(result.status == InstallDirectoryRegistrationStatus::failed,
        "invalid registry root should fail registration");
    Check(!result.error_message.empty(), "failure should include a diagnostic");
}

}  // namespace

// 运行安装路径登记的独立原生测试。
// argc：命令行参数数量；argv：命令行参数。
// 所有测试通过返回 EXIT_SUCCESS，任一断言失败返回 EXIT_FAILURE。
int wmain(int argc, wchar_t** argv) {
    static_cast<void>(argc);
    static_cast<void>(argv);
    const auto subkey = MakeRegistrySubkey();
    const auto root = std::filesystem::temp_directory_path() /
        (L"TimeCue 安装路径测试 " + std::to_wstring(GetCurrentProcessId()));
    DWORD environment_length = GetEnvironmentVariableW(L"TIMECUE_INSTALL_DIR", nullptr, 0);
    std::wstring previous_environment(environment_length, L'\0');
    if (environment_length != 0) {
        GetEnvironmentVariableW(L"TIMECUE_INSTALL_DIR", previous_environment.data(),
            environment_length);
        previous_environment.resize(wcslen(previous_environment.c_str()));
    }
    try {
        TestNonRelease(root, subkey + L"\\non-release");
        const auto release_executable = CreateReleaseDirectory(root / L"发行目录 初始");
        TestRegistration(root, subkey + L"\\registration");
        TestFailure(release_executable, subkey + L"\\restricted");
        Check(RemoveRegistryKey(subkey), "remove isolated test registry tree");
        std::filesystem::remove_all(root);
        SetEnvironmentVariableW(L"TIMECUE_INSTALL_DIR",
            previous_environment.empty() ? nullptr : previous_environment.c_str());
        std::cout << "install directory tests passed\n";
        return EXIT_SUCCESS;
    } catch (const std::exception& error) {
        RemoveRegistryKey(subkey);
        std::filesystem::remove_all(root);
        SetEnvironmentVariableW(L"TIMECUE_INSTALL_DIR",
            previous_environment.empty() ? nullptr : previous_environment.c_str());
        std::cerr << error.what() << '\n';
        return EXIT_FAILURE;
    }
}
