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

// 仅拥有本类创建的进程和句柄，业务状态与握手资格由 Dart 管理。
class ManagedProcess {
 public:
  explicit ManagedProcess(std::function<void()> wake = {});
  ~ManagedProcess();
  ManagedProcess(const ManagedProcess&) = delete;
  ManagedProcess& operator=(const ManagedProcess&) = delete;
  bool Start(const std::wstring& executable, const std::vector<std::wstring>& arguments,
             uint64_t generation, DWORD& pid, std::wstring& error);
  void Shutdown();
  bool Wait(DWORD milliseconds);
  bool Alive() const;
  void Release();
  std::vector<ProcessEvent> TakeEvents();
  static std::wstring QuoteArgument(const std::wstring& argument);

 private:
  void ReadPipe(HANDLE pipe, bool output);
  void WatchExit();
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
