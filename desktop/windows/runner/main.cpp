#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <windows.h>

#include "flutter_window.h"
#include "utils.h"
#include "single_instance.h"

int APIENTRY wWinMain(_In_ HINSTANCE instance, _In_opt_ HINSTANCE prev,
                      _In_ wchar_t *command_line, _In_ int show_command) {
  // 连接已有控制台（如 flutter run）；使用调试器启动时创建控制台。
  if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
    CreateAndAttachConsole();
  }

  // 业务装配之前取得主实例资格；发送端不创建 Flutter 引擎。
  SingleInstance single_instance;
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
  window.SetQuitOnClose(true);

  ::MSG msg;
  while (::GetMessage(&msg, nullptr, 0, 0)) {
    ::TranslateMessage(&msg);
    ::DispatchMessage(&msg);
  }

  ::CoUninitialize();
  return EXIT_SUCCESS;
}
