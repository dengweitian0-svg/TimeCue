#ifndef RUNNER_SINGLE_INSTANCE_H_
#define RUNNER_SINGLE_INSTANCE_H_

#include <windows.h>
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

// 协议编码独立于 Flutter，原生测试可直接验证边界和 Unicode。
bool ValidateDesktopLaunch(const DesktopLaunch& launch);
std::vector<uint8_t> EncodeDesktopLaunch(const DesktopLaunch& launch);
std::optional<DesktopLaunch> DecodeDesktopLaunch(const void* data, size_t size);

class SingleInstance {
 public:
  enum class Result { primary, forwarded, error };
  explicit SingleInstance(const std::wstring& suffix = L"");
  ~SingleInstance();
  SingleInstance(const SingleInstance&) = delete;
  SingleInstance& operator=(const SingleInstance&) = delete;
  Result Start(const std::vector<std::string>& arguments);
  std::vector<DesktopLaunch> Ready(std::function<void(const DesktopLaunch&)> handler);
  void BeginExit();
  const std::wstring& error() const { return error_; }
  HWND handle() const { return receiver_; }

 private:
  static LRESULT CALLBACK WindowProc(HWND window, UINT message, WPARAM wparam, LPARAM lparam);
  bool CreateReceiver(bool primary);
  bool Receive(HWND sender, const COPYDATASTRUCT* copy);
  HANDLE mutex_ = nullptr;
  HWND receiver_ = nullptr;
  bool owns_mutex_ = false, exiting_ = false;
  std::wstring identity_, class_name_, error_;
  DesktopLaunch initial_;
  std::optional<DesktopLaunch> pending_activation_, pending_open_;
  std::deque<uint64_t> recent_;
  std::function<void(const DesktopLaunch&)> handler_;
};

#endif
