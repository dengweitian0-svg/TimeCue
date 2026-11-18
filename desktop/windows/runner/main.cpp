#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <windows.h>

#include "flutter_window.h"
#include "utils.h"
#include "single_instance.h"
#include "install_directory.h"

// 取得主实例资格后创建 Flutter 窗口并运行 Windows 消息循环，二次启动仅转交参数。
// instance：当前模块
// prev：兼容性参数
// command_line：系统命令行
// show_command：系统初始显示建议
// 四者在此实现中均未直接使用，命令行由工具函数读取。
// 成功退出或转交成功返回 EXIT_SUCCESS；单实例或窗口创建失败返回 EXIT_FAILURE。
int APIENTRY wWinMain(
    _In_ HINSTANCE instance,
    _In_opt_ HINSTANCE prev,
    _In_ wchar_t *command_line,
    _In_ int show_command) {
    // 连接已有控制台（如 flutter run）；使用调试器启动时创建控制台。
    if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
        CreateAndAttachConsole();
    }

    // 业务装配之前取得主实例资格；发送端不创建 Flutter 引擎。
#ifdef TIMECUE_DEVELOPMENT
    // 开发构建与正式实例使用独立单实例锁及转交窗口。
    SingleInstance single_instance(L".Development");
#else
    SingleInstance single_instance;
#endif
    auto command_line_arguments = GetCommandLineArguments();
    const auto instance_result = single_instance.Start(command_line_arguments);
    if (instance_result == SingleInstance::Result::forwarded) return EXIT_SUCCESS;
    if (instance_result == SingleInstance::Result::error) {
        MessageBoxW(nullptr, single_instance.error().c_str(), L"TimeCue", MB_OK | MB_ICONERROR);
        return EXIT_FAILURE;
    }

    // 初始化 COM，供运行库和插件使用。
    ::CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);

    flutter::DartProject project(L"data");

    project.set_dart_entrypoint_arguments(std::move(command_line_arguments));

    FlutterWindow window(project, &single_instance);
    Win32Window::Point origin(10, 10);
    Win32Window::Size size(1280, 800);
    if (!window.Create(L"TimeCue", origin, size)) {
        return EXIT_FAILURE;
    }
    std::wstring executable_path;
    std::wstring registration_error;
    if (GetCurrentExecutablePath(&executable_path, &registration_error)) {
        const auto registration = RegisterInstallDirectory(executable_path);
        if (registration.status == InstallDirectoryRegistrationStatus::failed) {
            MessageBoxW(
                window.GetHandle(),
                registration.error_message.c_str(),
                L"TimeCue 安装路径登记失败",
                MB_OK | MB_ICONWARNING);
        }
    } else {
        MessageBoxW(
            window.GetHandle(),
            registration_error.c_str(),
            L"TimeCue 安装路径登记失败",
            MB_OK | MB_ICONWARNING);
    }
    window.SetQuitOnClose(true);

    ::MSG msg;
    while (::GetMessage(&msg, nullptr, 0, 0)) {
        ::TranslateMessage(&msg);
        ::DispatchMessage(&msg);
    }

    ::CoUninitialize();
    return EXIT_SUCCESS;
}
