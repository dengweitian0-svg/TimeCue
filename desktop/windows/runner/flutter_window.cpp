#include "flutter_window.h"
#include "desktop_window.h"

#include <flutter/standard_method_codec.h>
#include <shellapi.h>
#include <filesystem>
#include <fstream>
#include <thread>
#include <chrono>
#include <optional>
#include <cctype>

#include "flutter/generated_plugin_registrant.h"

namespace {
constexpr UINT kDestroyDesktop = WM_APP + 73;
constexpr UINT kProcessEvents = WM_APP + 74;
constexpr UINT kUpdateReady = WM_APP + 75;

// 把原生启动请求转为 Dart 激活信封，滤除后端及路径配置选项。
// launch：经过原生校验的启动请求。
// 返回包含版本、字符串 ID、来源和意图参数的 Flutter 可编码映射。
flutter::EncodableValue LaunchValue(const DesktopLaunch& launch) {
    flutter::EncodableList arguments;
    for (const auto& argument : launch.arguments) {
        if (argument.rfind("--backend=", 0) == 0 || argument.rfind("--data-dir=", 0) == 0 ||
            argument.rfind("--go-executable=", 0) == 0)
            continue;
        arguments.emplace_back(argument);
    }
    return flutter::EncodableValue(flutter::EncodableMap{
        { flutter::EncodableValue("version"), flutter::EncodableValue(1) },
        { flutter::EncodableValue("id"), flutter::EncodableValue(std::to_string(launch.id)) },
        { flutter::EncodableValue("source"),
            flutter::EncodableValue(
                launch.source == 1 ? "notification" : (launch.source == 2 ? "login" : "open")) },
        { flutter::EncodableValue("arguments"), flutter::EncodableValue(arguments) },
    });
}

// 读取当前 runner 所在目录，供安装器配置与应用一起安装的资源路径。
// 无参数。
// 返回应用绝对目录；路径读取失败时返回空串。
std::string InstallDirectory() {
    std::wstring path(32768, L'\0');
    const DWORD size = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
    if (!size || size >= path.size()) return "";
    path.resize(size);
    const auto directory = std::filesystem::path(path).parent_path().wstring();
    const int utf8_size = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS,
        directory.data(), static_cast<int>(directory.size()), nullptr, 0, nullptr, nullptr);
    if (!utf8_size) return "";
    std::string result(utf8_size, '\0');
    WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, directory.data(),
        static_cast<int>(directory.size()), result.data(), utf8_size, nullptr, nullptr);
    return result;
}

// 返回系统 Windows PowerShell 5.1 的绝对路径。
// 无参数。
// 返回可执行文件路径；系统目录读取失败时返回空串。
std::wstring WindowsPowerShellPath() {
    std::wstring windows(32768, L'\0');
    const UINT size = GetWindowsDirectoryW(windows.data(), static_cast<UINT>(windows.size()));
    if (!size || size >= windows.size()) return L"";
    windows.resize(size);
    return (std::filesystem::path(windows) / L"System32" / L"WindowsPowerShell" /
        L"v1.0" / L"powershell.exe").wstring();
}

// 从状态 JSON 中提取 state 字符串，避免匹配 message 等字段中的偶然文本。
// value：受限长度的 JSON 文本。
// 返回 state 字段；格式不符时返回空串。
std::string JsonState(const std::string& value) {
    const auto key = value.find("\"state\"");
    if (key == std::string::npos) return "";
    const auto colon = value.find(':', key + 7);
    if (colon == std::string::npos) return "";
    auto start = colon + 1;
    while (start < value.size() && std::isspace(static_cast<unsigned char>(value[start]))) ++start;
    if (start >= value.size() || value[start] != '"') return "";
    const auto end = value.find('"', start + 1);
    return end == std::string::npos ? "" : value.substr(start + 1, end - start - 1);
}

// 从更新状态 JSON 中读取供用户查看的 message 文本。
// value：受限长度的 JSON 文本。
// 返回解码后的 message；不存在或格式错误时返回空串。
std::string JsonMessage(const std::string& value) {
    const auto key = value.find("\"message\"");
    if (key == std::string::npos) return "";
    const auto colon = value.find(':', key + 9);
    if (colon == std::string::npos) return "";
    auto start = colon + 1;
    while (start < value.size() && std::isspace(static_cast<unsigned char>(value[start]))) ++start;
    if (start >= value.size() || value[start] != '"') return "";
    std::string message;
    for (auto index = start + 1; index < value.size(); ++index) {
        if (value[index] == '"') return message;
        if (value[index] == '\\' && index + 1 < value.size()) {
            const char escaped = value[++index];
            message.push_back(escaped == 'n' ? '\n' : (escaped == 'r' ? '\r' : escaped));
        } else {
            message.push_back(value[index]);
        }
    }
    return "";
}

// 读取受限大小的状态 JSON，避免异常文件占用过多内存。
// path：状态文件绝对路径。
// 返回最多 16 KiB 的文件内容；文件缺失返回空串，超限返回受限错误 JSON。
std::string ReadUpdateStatus(const std::filesystem::path& path) {
    std::ifstream input(path, std::ios::binary);
    if (!input) return "";
    std::string value(16 * 1024 + 1, '\0');
    input.read(value.data(), static_cast<std::streamsize>(value.size()));
    const auto size = static_cast<size_t>(input.gcount());
    if (size > 16 * 1024) {
        return "{\"state\":\"failed\",\"message\":\"更新状态文件超过大小限制。\"}";
    }
    value.resize(size);
    return value;
}

// 根据安装计划在助手工作区写入提交或取消标记。
// plan：安装计划绝对路径；commit：true 写提交，false 写取消。
// 返回 true 表示标记写入成功。
bool UpdateDecision(const std::wstring& plan, bool commit) {
    const auto directory = std::filesystem::path(plan).parent_path();
    const auto commit_path = directory / L"commit.marker";
    const auto cancel_path = directory / L"cancel.marker";
    std::error_code error;
    if (commit) {
        std::filesystem::remove(cancel_path, error);
        std::ofstream marker(commit_path, std::ios::binary | std::ios::trunc);
        marker << "commit\n";
        marker.flush();
        return marker.good();
    }
    std::filesystem::remove(commit_path, error);
    std::ofstream marker(cancel_path, std::ios::binary | std::ios::trunc);
    marker << "cancel\n";
    marker.flush();
    return marker.good();
}
}  // 匿名命名空间结束

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
        flutter_controller_->engine()->messenger(),
        "timecue/desktop",
        &flutter::StandardMethodCodec::GetInstance());
    // 用途：响应 Dart 的桌面就绪、显示、托盘检查及退出请求。
    // 入参：call：方法名及参数；result：结果回调，通过 Success、Error 或 NotImplemented 回传。
    // 出参：无直接返回值，结果经 result 交还 Dart。
    desktop_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
        if (call.method_name() == "ready") {
            host_ready_ = true;
            // 用途：将后续接纳的启动请求通过桌面通道推送给 Dart。
            // 入参：launch：已校验的启动请求。
            // 出参：无返回值。
            auto launches = instance_->Ready([this](const DesktopLaunch& launch) {
                if (!exiting_ && desktop_channel_) {
                    desktop_channel_->InvokeMethod(
                        "launch", std::make_unique<flutter::EncodableValue>(LaunchValue(launch)));
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
    // 用途：从后台线程投递进程事件消息，唤醒窗口线程取走队列。
    // 入参：无。
    // 出参：无返回值。
    managed_process_ = std::make_unique<ManagedProcess>([this] {
        PostMessageW(GetHandle(), kProcessEvents, 0, 0);
    });
    process_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
        flutter_controller_->engine()->messenger(),
        "timecue/process",
        &flutter::StandardMethodCodec::GetInstance());
    // 用途：响应 Dart 的配置读取和自有进程启动、停止、存活检查及回收请求。
    // 入参：call：方法名及参数；result：用于回传成功值或错误的结果回调。
    // 出参：无直接返回值，结果经 result 交还 Dart。
    process_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
        if (call.method_name() == "configuration") {
            const auto& configuration = instance_->configuration();
            result->Success(flutter::EncodableValue(flutter::EncodableMap{
                { flutter::EncodableValue("backend"), flutter::EncodableValue(configuration.backend) },
                { flutter::EncodableValue("dataDir"), flutter::EncodableValue(configuration.data_dir) },
                { flutter::EncodableValue("executable"),
                    flutter::EncodableValue(configuration.executable) } }));
        } else if (call.method_name() == "start") {
            if (exiting_) {
                result->Error("exiting", "宿主正在退出。");
                return;
            }
            const auto* arguments = std::get_if<flutter::EncodableMap>(call.arguments());
            // 用途：从当前方法参数映射中读取字符串字段。
            // 入参：key：要读取的有效字段名。
            // 出参：返回字段文本；参数缺失或类型不符时返回空串。
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
            if (!generation_text.empty() &&
                generation_text.find_first_not_of("0123456789") == std::string::npos) {
                try {
                    generation = std::stoull(generation_text);
                } catch (...) {
                }
            }
            const auto& configuration = instance_->configuration();
            if (configuration.backend != "managed" || configuration.data_dir.empty() ||
                configuration.executable.empty() || !generation || id.empty() || id.size() > 128 ||
                id.find_first_not_of(
                    "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != std::string::npos) {
                result->Error("configuration", "托管参数无效。");
                return;
            }
            DWORD pid = 0;
            std::wstring failure;
            if (!managed_process_->Start(
                    LaunchWide(configuration.executable),
                    { L"--data-dir",
                        LaunchWide(configuration.data_dir),
                        L"--instance-id",
                        LaunchWide(id) },
                    generation,
                    pid,
                    failure)) {
                const int size = WideCharToMultiByte(
                    CP_UTF8,
                    0,
                    failure.data(),
                    static_cast<int>(failure.size()),
                    nullptr,
                    0,
                    nullptr,
                    nullptr);
                std::string message(size, '\0');
                WideCharToMultiByte(
                    CP_UTF8,
                    0,
                    failure.data(),
                    static_cast<int>(failure.size()),
                    message.data(),
                    size,
                    nullptr,
                    nullptr);
                result->Error("start_failed", message);
                return;
            }
            result->Success(flutter::EncodableValue(static_cast<int64_t>(pid)));
        } else if (call.method_name() == "shutdown") {
            managed_process_->Shutdown();
            result->Success();
        } else if (call.method_name() == "alive") {
            result->Success(flutter::EncodableValue(managed_process_->Alive()));
        } else if (call.method_name() == "release") {
            managed_process_->Release();
            result->Success();
        } else {
            result->NotImplemented();
        }
    });
    update_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
        flutter_controller_->engine()->messenger(),
        "timecue/update",
        &flutter::StandardMethodCodec::GetInstance());
    // 用途：返回当前安装信息，或隐藏启动独立更新安装器并等待其就绪。
    // 入参：call：Dart 方法和参数；result：Flutter 方法结果回调。
    // 出参：配置信息或安装器就绪回执；启动或等待失败时返回错误。
    update_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
        if (call.method_name() == "configuration") {
            const auto& configuration = instance_->configuration();
            result->Success(flutter::EncodableValue(flutter::EncodableMap{
                { flutter::EncodableValue("backend"), flutter::EncodableValue(configuration.backend) },
                { flutter::EncodableValue("dataDir"), flutter::EncodableValue(configuration.data_dir) },
                { flutter::EncodableValue("executable"), flutter::EncodableValue(configuration.executable) },
                { flutter::EncodableValue("version"), flutter::EncodableValue(FLUTTER_VERSION) },
                { flutter::EncodableValue("installDirectory"), flutter::EncodableValue(InstallDirectory()) },
            }));
            return;
        }
        if (call.method_name() == "readUpdateStatus") {
            const auto& configuration = instance_->configuration();
            if (configuration.data_dir.empty()) {
                result->Success(flutter::EncodableValue());
                return;
            }
            const auto path = std::filesystem::path(LaunchWide(configuration.data_dir)) /
                L"software-update-status.json";
            const auto contents = ReadUpdateStatus(path);
            if (contents.empty()) {
                result->Success(flutter::EncodableValue());
                return;
            }
            result->Success(flutter::EncodableValue(contents));
            return;
        }
        if (call.method_name() == "commitUpdate" || call.method_name() == "cancelUpdate") {
            const auto* arguments = std::get_if<flutter::EncodableMap>(call.arguments());
            if (!arguments) {
                result->Error("update_path", "更新计划路径无效。");
                return;
            }
            const auto found = arguments->find(flutter::EncodableValue("planPath"));
            if (found == arguments->end() ||
                !std::holds_alternative<std::string>(found->second)) {
                result->Error("update_path", "更新计划路径无效。");
                return;
            }
            const auto plan = LaunchWide(std::get<std::string>(found->second));
            if (plan.empty() || !std::filesystem::path(plan).is_absolute() ||
                !UpdateDecision(plan, call.method_name() == "commitUpdate")) {
                result->Error("update_marker", "无法写入更新提交状态。");
                return;
            }
            result->Success();
            return;
        }
        const std::string mode = call.method_name() == "prepareUpdate" ? "Prepare" :
            (call.method_name() == "launchUpdate" ? "Install" : "");
        if (mode.empty()) {
            result->NotImplemented();
            return;
        }
        if (exiting_ || update_waiter_.joinable()) {
            result->Error("update_busy", "更新安装器已启动或宿主正在退出。");
            return;
        }
        const auto* arguments = std::get_if<flutter::EncodableMap>(call.arguments());
        auto text = [&](const char* key) -> std::string {
            if (!arguments) return "";
            const auto found = arguments->find(flutter::EncodableValue(key));
            if (found == arguments->end()) return "";
            const auto* value = std::get_if<std::string>(&found->second);
            return value ? *value : "";
        };
        const auto helper = LaunchWide(text("helperPath"));
        const auto plan = LaunchWide(text("planPath"));
        const auto powershell = WindowsPowerShellPath();
        if (helper.empty() || plan.empty() || !std::filesystem::path(helper).is_absolute() ||
            !std::filesystem::path(plan).is_absolute() || powershell.empty()) {
            result->Error("update_path", "更新脚本、计划或系统 PowerShell 路径无效。");
            return;
        }
        const auto status = std::filesystem::path(plan).parent_path() / L"status.json";
        std::error_code marker_error;
        if (mode == "Prepare") {
            std::filesystem::remove(status, marker_error);
            std::filesystem::remove(std::filesystem::path(plan).parent_path() / L"prepared.json", marker_error);
        } else {
            std::filesystem::remove(std::filesystem::path(plan).parent_path() / L"commit.marker", marker_error);
            std::filesystem::remove(std::filesystem::path(plan).parent_path() / L"cancel.marker", marker_error);
            std::filesystem::remove(status, marker_error);
        }
        const auto command = ManagedProcess::QuoteArgument(powershell) +
            L" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " +
            ManagedProcess::QuoteArgument(helper) + L" -Mode " +
            (mode == "Prepare" ? L"Prepare" : L"Install") + L" -PlanPath " +
            ManagedProcess::QuoteArgument(plan);
        std::vector<wchar_t> mutable_command(command.begin(), command.end());
        mutable_command.push_back(L'\0');
        STARTUPINFOW startup{};
        startup.cb = sizeof(startup);
        PROCESS_INFORMATION process{};
        const auto working_directory = std::filesystem::path(helper).parent_path().wstring();
        if (!CreateProcessW(powershell.c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                CREATE_NO_WINDOW | DETACHED_PROCESS, nullptr, working_directory.c_str(),
                &startup, &process)) {
            result->Error("update_launch", "无法启动更新脚本（Windows 错误 " +
                std::to_string(GetLastError()) + "）。");
            return;
        }
        CloseHandle(process.hThread);
        cancel_update_wait_ = false;
        update_result_ = std::move(result);
        update_result_boolean_ = mode == "Prepare";
        update_waiter_ = std::thread([this, process_handle = process.hProcess, status, plan, mode]() {
            std::string error;
            const auto timeout = mode == "Prepare" ? std::chrono::minutes(10) :
                std::chrono::seconds(30);
            const auto deadline = std::chrono::steady_clock::now() + timeout;
            bool child_exited = false;
            DWORD exit_code = STILL_ACTIVE;
            while (!cancel_update_wait_ && std::chrono::steady_clock::now() < deadline) {
                if (WaitForSingleObject(process_handle, 0) == WAIT_OBJECT_0) {
                    child_exited = true;
                    GetExitCodeProcess(process_handle, &exit_code);
                    if (mode == "Prepare") break;
                }
                const auto body = ReadUpdateStatus(status);
                if (!body.empty()) {
                    const auto state = JsonState(body);
                    if (mode == "Install" && state == "ready") break;
                    if (state == "failed") {
                        error = JsonMessage(body);
                        if (error.empty()) error = "更新脚本执行失败。";
                        if (mode == "Prepare") TerminateProcess(process_handle, 1);
                        break;
                    }
                }
                if (mode == "Install" && child_exited) {
                    error = "安装助手在进入等待状态前退出，代码 " +
                        std::to_string(exit_code) + "。";
                    break;
                }
                std::this_thread::sleep_for(std::chrono::milliseconds(100));
            }
            if (mode == "Prepare" && error.empty() && child_exited) {
                const auto contents = ReadUpdateStatus(status);
                if (exit_code != 0 || JsonState(contents) != "prepared") {
                    error = JsonMessage(contents);
                    if (error.empty()) {
                        error = "更新包校验或准备失败（退出代码 " +
                            std::to_string(exit_code) + "）。";
                    }
                }
            }
            if (mode == "Prepare" && error.empty() && !child_exited) {
                error = "更新准备操作超时。";
                TerminateProcess(process_handle, 1);
            }
            if (mode == "Install" && error.empty()) {
                const auto contents = ReadUpdateStatus(status);
                if (JsonState(contents) != "ready") error = "更新助手未能进入等待状态。";
            }
            if (mode == "Install" && !error.empty() && !cancel_update_wait_) {
                UpdateDecision(plan, false);
            }
            if (cancel_update_wait_) {
                error = "宿主已关闭，更新安装器就绪检查已取消。";
                if (mode == "Prepare") TerminateProcess(process_handle, 1);
                else UpdateDecision(plan, false);
            }
            CloseHandle(process_handle);
            {
                std::lock_guard<std::mutex> lock(update_mutex_);
                update_error_ = std::move(error);
            }
            if (!cancel_update_wait_) PostMessageW(GetHandle(), kUpdateReady, 0, 0);
        });
    });
    // 首次可见性由已初始化的 Dart 宿主决定，不再无条件首帧 Show。
    flutter_controller_->ForceRedraw();
    return true;
}

void FlutterWindow::OnDestroy() {
    // Win32Window::Create 首先调用空清理，只有实际引擎释放才结束接纳。
    if (flutter_controller_ || desktop_channel_) instance_->BeginExit();
    cancel_update_wait_ = true;
    if (update_waiter_.joinable()) update_waiter_.join();
    if (managed_process_) managed_process_->Release();
    if (update_channel_) {
        update_channel_->SetMethodCallHandler(nullptr);
        update_channel_ = nullptr;
    }
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

LRESULT FlutterWindow::MessageHandler(
    HWND hwnd, UINT message, WPARAM wparam, LPARAM lparam) noexcept {
    if (message == kUpdateReady) {
        if (update_waiter_.joinable()) update_waiter_.join();
        std::string error;
        {
            std::lock_guard<std::mutex> lock(update_mutex_);
            error = std::move(update_error_);
        }
        if (update_result_) {
            if (error.empty() && update_result_boolean_)
                update_result_->Success(flutter::EncodableValue(true));
            else if (error.empty()) update_result_->Success();
            else update_result_->Error("update_not_ready", error);
            update_result_.reset();
            update_result_boolean_ = false;
        }
        return 0;
    }
    if (message == kProcessEvents) {
        if (managed_process_ && process_channel_) {
            for (const auto& event : managed_process_->TakeEvents()) {
                process_channel_->InvokeMethod(
                    "event",
                    std::make_unique<flutter::EncodableValue>(flutter::EncodableMap{
                        { flutter::EncodableValue("generation"),
                            flutter::EncodableValue(std::to_string(event.generation)) },
                        { flutter::EncodableValue("type"), flutter::EncodableValue(event.type) },
                        { flutter::EncodableValue("bytes"), flutter::EncodableValue(event.bytes) },
                        { flutter::EncodableValue("exitCode"),
                            flutter::EncodableValue(static_cast<int64_t>(event.exit_code)) } }));
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
