#include "flutter_window.h"
#include "desktop_window.h"

#include <flutter/standard_method_codec.h>
#include <shellapi.h>
#include <optional>

#include "flutter/generated_plugin_registrant.h"

namespace {
constexpr UINT kDestroyDesktop = WM_APP + 73;
constexpr UINT kProcessEvents = WM_APP + 74;
flutter::EncodableValue LaunchValue(const DesktopLaunch& launch) {
  flutter::EncodableList arguments;
  for (const auto& argument : launch.arguments) {
    if (argument.rfind("--backend=", 0) == 0 || argument.rfind("--data-dir=", 0) == 0 ||
        argument.rfind("--go-executable=", 0) == 0) continue;
    arguments.emplace_back(argument);
  }
  return flutter::EncodableValue(flutter::EncodableMap{
      {flutter::EncodableValue("version"), flutter::EncodableValue(1)},
      {flutter::EncodableValue("id"), flutter::EncodableValue(std::to_string(launch.id))},
      {flutter::EncodableValue("source"), flutter::EncodableValue(
          launch.source == 1 ? "notification" : (launch.source == 2 ? "login" : "open"))},
      {flutter::EncodableValue("arguments"), flutter::EncodableValue(arguments)},
  });
}
}  // namespace

FlutterWindow::FlutterWindow(const flutter::DartProject& project, SingleInstance* instance)
    : instance_(instance), project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) return false;
  RECT frame = GetClientArea();
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  if (!flutter_controller_->engine() || !flutter_controller_->view()) return false;
  RegisterPlugins(flutter_controller_->engine());
  SetChildContent(flutter_controller_->view()->GetNativeWindow());
  desktop_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "timecue/desktop",
      &flutter::StandardMethodCodec::GetInstance());
  desktop_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
    if (call.method_name() == "ready") {
      host_ready_ = true;
      auto launches = instance_->Ready([this](const DesktopLaunch& launch) {
        if (!exiting_ && desktop_channel_) {
          desktop_channel_->InvokeMethod("launch", std::make_unique<flutter::EncodableValue>(LaunchValue(launch)));
        }
      });
      flutter::EncodableList values;
      for (const auto& launch : launches) values.push_back(LaunchValue(launch));
      result->Success(flutter::EncodableValue(values));
    } else if (call.method_name() == "showWindow") {
      result->Success(flutter::EncodableValue(ShowDesktopWindow(GetHandle())));
    } else if (call.method_name() == "hasTray") {
      // 插件忽略 Shell_NotifyIcon 的返回值，实际图标存在才允许隐藏。
      NOTIFYICONIDENTIFIER icon{};
      icon.cbSize = sizeof(icon);
      icon.hWnd = GetHandle();
      icon.uID = 1;
      RECT bounds{};
      const bool available = SUCCEEDED(Shell_NotifyIconGetRect(&icon, &bounds));
      result->Success(flutter::EncodableValue(available));
    } else if (call.method_name() == "beginExit") {
      exiting_ = true;
      instance_->BeginExit();
      result->Success();
    } else if (call.method_name() == "destroy") {
      exiting_ = true;
      instance_->BeginExit();
      result->Success();
      PostMessageW(GetHandle(), kDestroyDesktop, 0, 0);
    } else {
      result->NotImplemented();
    }
  });
  managed_process_ = std::make_unique<ManagedProcess>([this] {
    PostMessageW(GetHandle(), kProcessEvents, 0, 0);
  });
  process_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "timecue/process",
      &flutter::StandardMethodCodec::GetInstance());
  process_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
    if (call.method_name() == "configuration") {
      const auto& configuration = instance_->configuration();
      result->Success(flutter::EncodableValue(flutter::EncodableMap{
          {flutter::EncodableValue("backend"), flutter::EncodableValue(configuration.backend)},
          {flutter::EncodableValue("dataDir"), flutter::EncodableValue(configuration.data_dir)},
          {flutter::EncodableValue("executable"), flutter::EncodableValue(configuration.executable)}}));
    } else if (call.method_name() == "start") {
      if (exiting_) { result->Error("exiting", "宿主正在退出。"); return; }
      const auto* arguments = std::get_if<flutter::EncodableMap>(call.arguments());
      auto text = [&](const char* key) -> std::string {
        if (!arguments) return "";
        const auto found = arguments->find(flutter::EncodableValue(key));
        if (found == arguments->end()) return "";
        const auto* value = std::get_if<std::string>(&found->second);
        return value ? *value : "";
      };
      const auto id = text("instanceId");
      const auto generation_text = text("generation");
      uint64_t generation = 0;
      if (!generation_text.empty() && generation_text.find_first_not_of("0123456789") == std::string::npos) {
        try { generation = std::stoull(generation_text); } catch (...) {}
      }
      const auto& configuration = instance_->configuration();
      if (configuration.backend != "managed" || configuration.data_dir.empty() ||
          configuration.executable.empty() || !generation || id.empty() || id.size() > 128 ||
          id.find_first_not_of("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != std::string::npos) {
        result->Error("configuration", "托管参数无效。"); return;
      }
      DWORD pid = 0;
      std::wstring failure;
      if (!managed_process_->Start(LaunchWide(configuration.executable),
          {L"--data-dir", LaunchWide(configuration.data_dir), L"--instance-id", LaunchWide(id)},
          generation, pid, failure)) {
        const int size = WideCharToMultiByte(CP_UTF8, 0, failure.data(), static_cast<int>(failure.size()),
            nullptr, 0, nullptr, nullptr);
        std::string message(size, '\0');
        WideCharToMultiByte(CP_UTF8, 0, failure.data(), static_cast<int>(failure.size()),
            message.data(), size, nullptr, nullptr);
        result->Error("start_failed", message);
        return;
      }
      result->Success(flutter::EncodableValue(static_cast<int64_t>(pid)));
    } else if (call.method_name() == "shutdown") {
      managed_process_->Shutdown(); result->Success();
    } else if (call.method_name() == "alive") {
      result->Success(flutter::EncodableValue(managed_process_->Alive()));
    } else if (call.method_name() == "release") {
      managed_process_->Release(); result->Success();
    } else {
      result->NotImplemented();
    }
  });
  // 首次可见性由已初始化的 Dart 宿主决定，不再无条件首帧 Show。
  flutter_controller_->ForceRedraw();
  return true;
}

void FlutterWindow::OnDestroy() {
  // Win32Window::Create 首先调用空清理，只有实际引擎释放才结束接纳。
  if (flutter_controller_ || desktop_channel_) instance_->BeginExit();
  if (managed_process_) managed_process_->Release();
  if (process_channel_) {
    process_channel_->SetMethodCallHandler(nullptr);
    process_channel_ = nullptr;
  }
  managed_process_ = nullptr;
  if (desktop_channel_) {
    desktop_channel_->SetMethodCallHandler(nullptr);
    desktop_channel_ = nullptr;
  }
  if (flutter_controller_) flutter_controller_ = nullptr;
  Win32Window::OnDestroy();
}

LRESULT FlutterWindow::MessageHandler(HWND hwnd, UINT message,
                                     WPARAM wparam, LPARAM lparam) noexcept {
  if (message == kProcessEvents) {
    if (managed_process_ && process_channel_) {
      for (const auto& event : managed_process_->TakeEvents()) {
        process_channel_->InvokeMethod("event", std::make_unique<flutter::EncodableValue>(flutter::EncodableMap{
            {flutter::EncodableValue("generation"), flutter::EncodableValue(std::to_string(event.generation))},
            {flutter::EncodableValue("type"), flutter::EncodableValue(event.type)},
            {flutter::EncodableValue("bytes"), flutter::EncodableValue(event.bytes)},
            {flutter::EncodableValue("exitCode"), flutter::EncodableValue(static_cast<int64_t>(event.exit_code))}}));
      }
    }
    return 0;
  }
  if (message == kDestroyDesktop) {
    if (exiting_) Destroy();
    return 0;
  }
  // 防止插件关闭拦截尚未就绪，或退出中再次关闭导致提前销毁。
  if (message == WM_CLOSE && (!host_ready_ || exiting_)) return 0;
  if (taskbar_created_ != 0 && message == taskbar_created_ && !exiting_ && host_ready_) {
    // 系统托盘重建期间先确保窗口可达；Dart 随后重试并核验图标。
    ShowWindow(hwnd, IsIconic(hwnd) ? SW_RESTORE : SW_SHOW);
    if (desktop_channel_) desktop_channel_->InvokeMethod("trayLost", nullptr);
  }
  if (flutter_controller_) {
    auto result = flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam, lparam);
    if (result) return *result;
  }
  if (message == WM_FONTCHANGE && flutter_controller_) {
    flutter_controller_->engine()->ReloadSystemFonts();
  }
  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
