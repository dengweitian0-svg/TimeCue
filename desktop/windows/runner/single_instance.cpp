#include "single_instance.h"

#include <sddl.h>
#include <algorithm>
#include <cstring>

namespace {
constexpr uint32_t kMagic = 0x54435137;
constexpr size_t kMaxBytes = 32768;
constexpr size_t kMaxArguments = 128;
constexpr ULONGLONG kForwardBudget = 2000;

std::wstring UserSid(DWORD process_id) {
  HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, process_id);
  if (!process) return L"";
  HANDLE token = nullptr;
  if (!OpenProcessToken(process, TOKEN_QUERY, &token)) { CloseHandle(process); return L""; }
  CloseHandle(process);
  DWORD size = 0;
  GetTokenInformation(token, TokenUser, nullptr, 0, &size);
  std::vector<uint8_t> bytes(size);
  std::wstring result;
  if (size && GetTokenInformation(token, TokenUser, bytes.data(), size, &size)) {
    LPWSTR sid = nullptr;
    if (ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(bytes.data())->User.Sid, &sid)) {
      result = sid;
      LocalFree(sid);
    }
  }
  CloseHandle(token);
  return result;
}

bool SameUserSession(HWND window) {
  DWORD pid = 0, session = 0, own_session = 0;
  if (!window || !GetWindowThreadProcessId(window, &pid) || !pid) return false;
  return ProcessIdToSessionId(pid, &session) &&
      ProcessIdToSessionId(GetCurrentProcessId(), &own_session) && session == own_session &&
      UserSid(pid) == UserSid(GetCurrentProcessId());
}

bool ValidUtf8(const std::string& value) {
  if (value.find('\0') != std::string::npos) return false;
  return value.empty() || MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), nullptr, 0) > 0;
}

void Append32(std::vector<uint8_t>& output, uint32_t value) {
  for (int i = 0; i < 4; ++i) output.push_back(static_cast<uint8_t>(value >> (i * 8)));
}

bool Read32(const uint8_t*& current, const uint8_t* end, uint32_t& value) {
  if (end - current < 4) return false;
  value = 0;
  for (int i = 0; i < 4; ++i) value |= static_cast<uint32_t>(*current++) << (i * 8);
  return true;
}
}  // namespace

bool ValidateDesktopLaunch(const DesktopLaunch& launch) {
  if (!launch.id || launch.source > 2 || launch.arguments.size() > kMaxArguments) return false;
  bool notification = false, login = false, hidden = false;
  size_t size = 24;
  for (const auto& argument : launch.arguments) {
    if (argument.size() > kMaxBytes || !ValidUtf8(argument)) return false;
    size += 4 + argument.size();
    if (size > kMaxBytes) return false;
    if (argument == "--login-start") {
      if (login) return false;
      login = true;
    } else if (argument == "--start-hidden") {
      if (hidden) return false;
      hidden = true;
    } else if (argument.rfind("--activation-payload=", 0) == 0) {
      if (notification) return false;
      notification = true;
    } else {
      return false;
    }
  }
  return launch.source == (notification ? 1u : (login ? 2u : 0u));
}

std::vector<uint8_t> EncodeDesktopLaunch(const DesktopLaunch& launch) {
  if (!ValidateDesktopLaunch(launch)) return {};
  std::vector<uint8_t> output;
  Append32(output, kMagic);
  Append32(output, 1);
  Append32(output, static_cast<uint32_t>(launch.id));
  Append32(output, static_cast<uint32_t>(launch.id >> 32));
  Append32(output, launch.source);
  Append32(output, static_cast<uint32_t>(launch.arguments.size()));
  for (const auto& argument : launch.arguments) {
    Append32(output, static_cast<uint32_t>(argument.size()));
    output.insert(output.end(), argument.begin(), argument.end());
  }
  return output;
}

std::optional<DesktopLaunch> DecodeDesktopLaunch(const void* data, size_t size) {
  if (!data || size < 24 || size > kMaxBytes) return std::nullopt;
  const auto* current = static_cast<const uint8_t*>(data);
  const auto* end = current + size;
  uint32_t magic = 0, version = 0, low = 0, high = 0, source = 0, count = 0;
  if (!Read32(current, end, magic) || !Read32(current, end, version) || magic != kMagic || version != 1 ||
      !Read32(current, end, low) || !Read32(current, end, high) ||
      !Read32(current, end, source) || !Read32(current, end, count) || count > kMaxArguments) return std::nullopt;
  DesktopLaunch result;
  result.id = static_cast<uint64_t>(low) | (static_cast<uint64_t>(high) << 32);
  result.source = source;
  for (uint32_t i = 0; i < count; ++i) {
    uint32_t length = 0;
    if (!Read32(current, end, length) || length > static_cast<size_t>(end - current)) return std::nullopt;
    result.arguments.emplace_back(reinterpret_cast<const char*>(current), length);
    current += length;
  }
  if (current != end || !ValidateDesktopLaunch(result)) return std::nullopt;
  return result;
}

SingleInstance::SingleInstance(const std::wstring& suffix) {
  DWORD session = 0;
  ProcessIdToSessionId(GetCurrentProcessId(), &session);
  const auto sid = UserSid(GetCurrentProcessId());
  if (sid.empty()) { error_ = L"无法核验当前 Windows 用户。"; return; }
  identity_ = L"TimeCue.Desktop." + sid + L"." + std::to_wstring(session) + suffix;
  class_name_ = identity_ + L".IPC";
}

SingleInstance::~SingleInstance() {
  if (receiver_) DestroyWindow(receiver_);
  if (!class_name_.empty()) UnregisterClassW(class_name_.c_str(), GetModuleHandle(nullptr));
  if (owns_mutex_) ReleaseMutex(mutex_);
  if (mutex_) CloseHandle(mutex_);
}

bool SingleInstance::CreateReceiver(bool primary) {
  WNDCLASSW window_class{};
  window_class.lpfnWndProc = WindowProc;
  window_class.hInstance = GetModuleHandle(nullptr);
  window_class.lpszClassName = class_name_.c_str();
  if (!RegisterClassW(&window_class) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) return false;
  // 顶层隐藏窗口能接收本会话的消息，发送端使用独立标题。
  receiver_ = CreateWindowW(class_name_.c_str(), primary ? identity_.c_str() : L"TimeCue sender",
      0, 0, 0, 0, 0, nullptr, nullptr, GetModuleHandle(nullptr), this);
  return receiver_ != nullptr;
}

SingleInstance::Result SingleInstance::Start(const std::vector<std::string>& arguments) {
  if (!error_.empty()) return Result::error;
  LARGE_INTEGER clock{};
  QueryPerformanceCounter(&clock);
  initial_.id = static_cast<uint64_t>(clock.QuadPart) ^ (static_cast<uint64_t>(GetCurrentProcessId()) << 32);
  if (!initial_.id) initial_.id = 1;
  initial_.arguments = arguments;
  for (const auto& argument : arguments) {
    if (argument == "--login-start") initial_.source = 2;
    if (argument.rfind("--activation-payload=", 0) == 0) { initial_.source = 1; break; }
  }
  const auto bytes = EncodeDesktopLaunch(initial_);
  if (bytes.empty()) { error_ = L"启动参数无效；不能通过重复启动更改目录、连接或模式。"; return Result::error; }
  const auto name = L"Local\\" + identity_;
  mutex_ = CreateMutexW(nullptr, FALSE, name.c_str());
  if (!mutex_) { error_ = L"无法取得 TimeCue 单实例锁。"; return Result::error; }
  const ULONGLONG deadline = GetTickCount64() + kForwardBudget;
  while (GetTickCount64() < deadline) {
    const DWORD acquired = WaitForSingleObject(mutex_, 0);
    if (acquired == WAIT_OBJECT_0 || acquired == WAIT_ABANDONED) {
      owns_mutex_ = true;
      if (!CreateReceiver(true)) { error_ = L"无法创建 TimeCue 激活接收端。"; return Result::error; }
      return Result::primary;
    }
    if (acquired != WAIT_TIMEOUT) { error_ = L"TimeCue 实例锁状态无效。"; return Result::error; }
    HWND target = FindWindowW(class_name_.c_str(), identity_.c_str());
    if (target) {
      if (!SameUserSession(target)) { error_ = L"TimeCue 接收端身份不匹配。"; return Result::error; }
      if (!receiver_ && !CreateReceiver(false)) { error_ = L"无法创建参数发送端。"; return Result::error; }
      DWORD pid = 0;
      GetWindowThreadProcessId(target, &pid);
      AllowSetForegroundWindow(pid);
      COPYDATASTRUCT copy{};
      copy.dwData = kMagic;
      copy.cbData = static_cast<DWORD>(bytes.size());
      copy.lpData = const_cast<uint8_t*>(bytes.data());
      DWORD_PTR acknowledgement = 0;
      const auto now = GetTickCount64();
      const UINT remaining = static_cast<UINT>(deadline > now ? deadline - now : 0);
      if (!remaining) break;
      if (SendMessageTimeoutW(target, WM_COPYDATA, reinterpret_cast<WPARAM>(receiver_),
          reinterpret_cast<LPARAM>(&copy), SMTO_ABORTIFHUNG | SMTO_BLOCK | SMTO_ERRORONEXIT,
          remaining, &acknowledgement)) {
        if (acknowledgement == 1) return Result::forwarded;
        error_ = L"TimeCue 正在退出或无法接纳启动请求。";
        return Result::error;
      }
      // 超时不选举第二执行者；只有实际取得锁所有权才允许继续启动。
    }
    Sleep(10);
  }
  error_ = L"已有 TimeCue 未在 2 秒内接纳启动请求，请稍后重试。";
  return Result::error;
}

bool SingleInstance::Receive(HWND sender, const COPYDATASTRUCT* copy) {
  if (!owns_mutex_ || exiting_ || !copy || copy->dwData != kMagic || !SameUserSession(sender)) return false;
  auto launch = DecodeDesktopLaunch(copy->lpData, copy->cbData);
  if (!launch) return false;
  if (std::find(recent_.begin(), recent_.end(), launch->id) != recent_.end()) return true;
  recent_.push_back(launch->id);
  if (recent_.size() > 128) recent_.pop_front();
  if (handler_) {
    handler_(*launch);
  } else if (launch->source == 1) {
    pending_activation_ = std::move(launch);
  } else if (launch->source == 0) {
    pending_open_ = std::move(launch);
  }
  return true;
}

std::vector<DesktopLaunch> SingleInstance::Ready(std::function<void(const DesktopLaunch&)> handler) {
  handler_ = std::move(handler);
  std::vector<DesktopLaunch> result{initial_};
  if (pending_open_) result.push_back(*pending_open_);
  if (pending_activation_) result.push_back(*pending_activation_);
  pending_open_.reset();
  pending_activation_.reset();
  return result;
}

void SingleInstance::BeginExit() { exiting_ = true; handler_ = nullptr; }

LRESULT CALLBACK SingleInstance::WindowProc(HWND window, UINT message, WPARAM wparam, LPARAM lparam) {
  auto* instance = reinterpret_cast<SingleInstance*>(GetWindowLongPtrW(window, GWLP_USERDATA));
  if (message == WM_NCCREATE) {
    instance = static_cast<SingleInstance*>(reinterpret_cast<CREATESTRUCTW*>(lparam)->lpCreateParams);
    SetWindowLongPtrW(window, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(instance));
  }
  if (message == WM_COPYDATA && instance) {
    return instance->Receive(reinterpret_cast<HWND>(wparam), reinterpret_cast<COPYDATASTRUCT*>(lparam)) ? 1 : 0;
  }
  return DefWindowProcW(window, message, wparam, lparam);
}
