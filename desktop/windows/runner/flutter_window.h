#ifndef RUNNER_FLUTTER_WINDOW_H_
#define RUNNER_FLUTTER_WINDOW_H_

#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>

#include <memory>
#include <atomic>
#include <mutex>
#include <thread>
#include <flutter/method_channel.h>
#include <flutter/encodable_value.h>
#include "single_instance.h"
#include "managed_process.h"

#include "win32_window.h"

// 承载 Flutter 视图并接入原生桌面与进程通道的窗口。
class FlutterWindow : public Win32Window {
public:
    // 保存 Flutter 项目和单实例管理器，准备承载引擎的窗口。
    // project：复制保存的 Dart 项目配置
    // instance：借用的单实例管理器，生命周期须覆盖窗口
    explicit FlutterWindow(const flutter::DartProject& project, SingleInstance* instance);

    // 正常销毁时资源清理由 OnDestroy 执行。
    virtual ~FlutterWindow();

protected:
    // 创建 Flutter 引擎和视图，注册插件、桌面及进程通道，装配自有进程管理器。
    // 返回 true 表示窗口装配完成；false 表示基类或引擎/视图初始化失败。
    bool OnCreate() override;

    // 停止接纳请求，回收自有进程、方法通道及 Flutter 引擎，再调用基类清理。
    void OnDestroy() override;

    // 回传进程事件，处理销毁和托盘丢失消息，交由 Flutter 插件或基类处理其他消息。
    // hwnd：目标窗口
    // message：消息编号
    // wparam、lparam：随消息类型解释的 Win32 参数。
    // 返回 Win32 消息结果；若已消费的宿主消息返回 0。
    LRESULT MessageHandler(
        HWND window, UINT const message, WPARAM const wparam, LPARAM const lparam) noexcept override;

private:
    SingleInstance* instance_;
    std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> desktop_channel_;
    std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> process_channel_;
    std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> update_channel_;
    std::unique_ptr<ManagedProcess> managed_process_;
    std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> update_result_;
    std::thread update_waiter_;
    std::mutex update_mutex_;
    std::string update_error_;
    bool update_result_boolean_ = false;
    std::atomic_bool cancel_update_wait_{false};
    bool host_ready_ = false;
    bool exiting_ = false;
    UINT taskbar_created_ = RegisterWindowMessageW(L"TaskbarCreated");

    // 窗口运行的 Dart 项目配置。
    flutter::DartProject project_;

    // 当前窗口承载的 Flutter 引擎及视图控制器。
    std::unique_ptr<flutter::FlutterViewController> flutter_controller_;
};

#endif  // 头文件保护结束：RUNNER_FLUTTER_WINDOW_H_
