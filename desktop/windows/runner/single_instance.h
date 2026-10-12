#ifndef RUNNER_SINGLE_INSTANCE_H_
#define RUNNER_SINGLE_INSTANCE_H_

#include <windows.h>
#include "launch_configuration.h"
#include <cstdint>
#include <deque>
#include <functional>
#include <optional>
#include <string>
#include <vector>

struct DesktopLaunch {
    uint64_t id = 0;
    uint32_t source = 0;
    std::vector<std::string> arguments;
};

// 校验启动请求的标识、来源、参数白名单、UTF-8 及协议容量
// launch：待验证的启动请求
// 返回 true 表示请求合法；false 表示不能编码或接纳
bool ValidateDesktopLaunch(const DesktopLaunch& launch);

// 将合法启动请求编码为保留参数边界的版本化小端协议
// launch：待转交的启动请求
// 返回协议字节；校验失败时返回空列表
std::vector<uint8_t> EncodeDesktopLaunch(const DesktopLaunch& launch);

// 有界解码并校验启动协议，拒绝截断、超限和多余字节。
// data：借用的协议缓冲区
// size：缓冲区字节数。
// 返回解码后的请求；无效数据返回 std::nullopt。
std::optional<DesktopLaunch> DecodeDesktopLaunch(const void* data, size_t size);

class SingleInstance {
public:
    enum class Result { primary,
        forwarded,
        error };
    // 按当前用户 SID、会话及可选后缀生成单实例锁和 IPC 窗口标识
    // suffix：隔离实例使用的后缀，默认空串，原生测试使用独立后缀
    // 构造函数无返回值；身份核验失败记录到 error()，尚不取得锁
    explicit SingleInstance(const std::wstring& suffix = L"");

    // 销毁接收窗口、注销窗口类并释放所持有的 mutex 及句柄
    ~SingleInstance();
    SingleInstance(const SingleInstance&) = delete;
    SingleInstance& operator=(const SingleInstance&) = delete;

    // 解析并固定启动配置，在两秒预算内取得主实例资格或转交启动请求
    // arguments：不含程序名的 UTF-8 启动参数
    // 返回 primary、forwarded 或 error；失败详情由 error() 提供
    Result Start(const std::vector<std::string>& arguments);

    // 注册后续启动请求处理器，取走首次请求及就绪前暂存的打开和通知请求
    // handler：接纳请求时在接收窗口线程执行的回调，参数为只读启动请求
    // 返回首次及暂存请求列表；暂存项随后清空
    std::vector<DesktopLaunch> Ready(std::function<void(const DesktopLaunch&)> handler);

    // 标记退出并撤销请求处理器，使接收端拒绝新的启动请求
    void BeginExit();

    // 提供最近记录的原生启动错误说明
    // 返回只读宽字符串引用；有效期受当前对象生命周期约束
    const std::wstring& error() const { return error_; }

    // 提供本实例的隐藏 IPC 窗口句柄
    // 返回借用的 HWND；未创建时为 nullptr，调用方不得销毁
    HWND handle() const { return receiver_; }

    // 提供首次启动时固定的原生配置快照。
    const LaunchConfiguration& configuration() const { return configuration_; }

private:
    // 将隐藏窗口绑定到实例，并将 WM_COPYDATA 转交给接纳逻辑。
    // window：接收窗口
    // message：消息编号
    // wparam：WM_COPYDATA 时为发送端窗口
    // lparam：该消息时为 COPYDATASTRUCT 指针
    // 返回 1 表示接纳、0 表示拒绝；其他消息返回系统处理结果
    static LRESULT CALLBACK WindowProc(HWND window, UINT message, WPARAM wparam, LPARAM lparam);

    // 注册 IPC 窗口类并创建用于接收或发送请求的隐藏窗口
    // primary：true 使用主实例标题，false 使用发送端标题
    // 返回 true 表示窗口创建成功
    bool CreateReceiver(bool primary);

    // 验证发送方身份及配置，去重后分发或有界暂存启动请求
    // sender：发送端窗口
    // copy：借用的 WM_COPYDATA 协议描述，只在当前消息内使用
    // 返回 true 表示接纳或已处理过该请求；false 表示拒绝
    bool Receive(HWND sender, const COPYDATASTRUCT* copy);
    HANDLE mutex_ = nullptr;
    HWND receiver_ = nullptr;
    bool owns_mutex_ = false, exiting_ = false;
    std::wstring identity_, class_name_, error_;
    DesktopLaunch initial_;
    LaunchConfiguration configuration_;
    std::optional<DesktopLaunch> pending_activation_, pending_open_;
    std::deque<uint64_t> recent_;
    std::function<void(const DesktopLaunch&)> handler_;
};

#endif
