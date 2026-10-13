#ifndef RUNNER_INSTALL_DIRECTORY_H_
#define RUNNER_INSTALL_DIRECTORY_H_

#include <windows.h>

#include <string>

enum class InstallDirectoryRegistrationStatus {
    registered,
    unchanged,
    not_release,
    failed,
};

struct InstallDirectoryRegistrationOptions {
    HKEY registry_root = HKEY_CURRENT_USER;
    std::wstring registry_subkey = L"Environment";
    bool broadcast_change = true;
};

struct InstallDirectoryRegistrationResult {
    InstallDirectoryRegistrationStatus status = InstallDirectoryRegistrationStatus::failed;
    std::wstring error_message;
};

// 读取当前进程模块的完整 Windows 路径。
// executable_path：接收路径的输出字符串。
// 成功返回 true；失败返回 false 并设置系统错误说明。
bool GetCurrentExecutablePath(std::wstring* executable_path, std::wstring* error_message);

// 登记给定可执行文件所在的正式发行目录；选项可指定注册表根/子项并关闭广播供隔离测试使用。
// executable_path：可执行文件的绝对路径；options：注册表目标和是否广播设置变更。
// 返回登记、值未变化、非发行目录或带错误说明的失败结果。
InstallDirectoryRegistrationResult RegisterInstallDirectory(
    const std::wstring& executable_path,
    const InstallDirectoryRegistrationOptions& options = {});

#endif  // RUNNER_INSTALL_DIRECTORY_H_
