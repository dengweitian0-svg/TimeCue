#ifndef RUNNER_MANAGED_PROCESS_H_
#define RUNNER_MANAGED_PROCESS_H_

#include <windows.h>
#include <cstdint>
#include <deque>
#include <functional>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

struct ProcessEvent {
    uint64_t generation = 0;
    std::string type;
    std::vector<uint8_t> bytes;
    DWORD exit_code = 0;
};

// 仅拥有本类创建的进程和句柄，业务状态与握手资格由 Dart 管理
class ManagedProcess {
public:
    // 保存事件唤醒回调，初始化自有进程管理器。
    // wake：队列由空变为非空时调用的回调，可为空；可能在后台线程执行
    explicit ManagedProcess(std::function<void()> wake = {});

    // 回收自有进程、Job、管道和后台线程
    ~ManagedProcess();

    ManagedProcess(const ManagedProcess&) = delete;
    ManagedProcess& operator=(const ManagedProcess&) = delete;

    // 隐藏创建 Go 进程，在创建时关联 Job 并启动输出与退出观察线程。
    // executable：程序路径
    // arguments：不含程序名的参数
    // generation：事件轮次
    // pid：成功时写入进程 ID
    // error：失败时写入原因。
    // 返回 true 表示进程及观察线程已启动；false 表示失败；不代表业务握手已经就绪。
    bool Start(
        const std::wstring& executable,
        const std::vector<std::wstring>& arguments,
        uint64_t generation,
        DWORD& pid,
        std::wstring& error);

    // 向自有 Go 写入 shutdown 消息并关闭输入管道，以 EOF 补充取消信号。
    void Shutdown();

    // 在指定预算内等待自有进程退出。
    // milliseconds：等待时长，单位毫秒，可为零。
    // 返回 true 表示没有进程或进程已退出；false 表示超时或等待失败。
    bool Wait(DWORD milliseconds);

    // 以零等待检查自有进程是否仍在运行。
    // 返回 true 表示进程句柄存在且尚未退出。
    bool Alive() const;

    // 用途：幂等关闭 Job 以终止自有进程，并等待后台线程结束、关闭剩余句柄。
    void Release();

    // 在锁内取走全部待回传事件，并清空队列及字节计数。
    // 出参：返回按入队顺序排列的事件；无事件时返回空列表。
    std::vector<ProcessEvent> TakeEvents();

    // 按 Windows 命令行规则包裹并转义单个参数，保留引号及末尾反斜杠。
    // argument：未经转义的宽字符串参数。
    // 返回带双引号的命令行片段。
    static std::wstring QuoteArgument(const std::wstring& argument);

private:
    // 后台读取输出管道，将数据或 EOF 事件入队，取消时停止
    // pipe：借用的读取句柄，不在此关闭
    // output：true 表示 stdout，false 表示 stderr
    void ReadPipe(HANDLE pipe, bool output);

    // 等待进程退出或取消，正常观察到退出时回传退出码事件
    void WatchExit();

    // 在锁内按容量限制入队事件，必要时唤醒宿主处理，stdout 超限产生错误事件
    // event：携带轮次、类型、输出字节或退出码的事件，按值接收后移入队列
    void Emit(ProcessEvent event);
    std::function<void()> wake_;
    HANDLE process_ = nullptr, job_ = nullptr, input_ = nullptr;
    HANDLE output_ = nullptr, error_ = nullptr, cancel_ = nullptr;
    uint64_t generation_ = 0;
    std::thread output_thread_, error_thread_, exit_thread_;
    std::mutex events_mutex_;
    std::deque<ProcessEvent> events_;
    size_t queued_bytes_ = 0;
    bool overflow_ = false;
};
#endif
