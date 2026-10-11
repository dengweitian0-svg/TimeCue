#include "single_instance.h"
#include "desktop_window.h"
#include <shellapi.h>
#include <cstdio>
#include <stdexcept>

namespace {
constexpr ULONG_PTR kMagic = 0x54435137;
const std::string kPayload = "--activation-payload={\"名称\":\"中文 空格 \\\"引号\\\" C:\\\\目录\"}";
void Require(bool value, const char* message) {
  if (!value) throw std::runtime_error(message);
}
void Pump() {
  MSG message{};
  while (PeekMessageW(&message, nullptr, 0, 0, PM_REMOVE)) {
    TranslateMessage(&message);
    DispatchMessageW(&message);
  }
}
PROCESS_INFORMATION Spawn(const std::wstring& mode, const std::wstring& key) {
  wchar_t path[32768]{};
  GetModuleFileNameW(nullptr, path, 32768);
  std::wstring command = L"\"" + std::wstring(path) + L"\" " + mode + L" " + key;
  STARTUPINFOW startup{};
  startup.cb = sizeof(startup);
  PROCESS_INFORMATION process{};
  Require(CreateProcessW(path, command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
      nullptr, nullptr, &startup, &process) != FALSE, "spawn child");
  CloseHandle(process.hThread);
  return process;
}
DWORD WaitChild(PROCESS_INFORMATION process) {
  const auto deadline = GetTickCount64() + 6000;
  while (WaitForSingleObject(process.hProcess, 0) == WAIT_TIMEOUT && GetTickCount64() < deadline) {
    MsgWaitForMultipleObjects(1, &process.hProcess, FALSE, 50, QS_ALLINPUT);
    Pump();
  }
  DWORD code = STILL_ACTIVE;
  GetExitCodeProcess(process.hProcess, &code);
  if (code == STILL_ACTIVE) TerminateProcess(process.hProcess, 99);
  CloseHandle(process.hProcess);
  Require(code != STILL_ACTIVE, "child timeout");
  return code;
}
std::wstring EventName(const std::wstring& key) { return L"Local\\TimeCueD7Test" + key; }

int Child(const std::wstring& mode, const std::wstring& key) {
  HANDLE event = nullptr;
  if (mode == L"race") {
    event = OpenEventW(SYNCHRONIZE, FALSE, EventName(key).c_str());
    if (!event || WaitForSingleObject(event, 4000) != WAIT_OBJECT_0) return 90;
    CloseHandle(event);
  }
  SingleInstance instance(key);
  const auto result = instance.Start(mode == L"client" ? std::vector<std::string>{kPayload} : std::vector<std::string>{});
  if (mode == L"client") return result == SingleInstance::Result::forwarded ? 0 : 2;
  if (mode == L"race" && result == SingleInstance::Result::forwarded) return 101;
  if (result != SingleInstance::Result::primary) return 91;
  bool received = false;
  instance.Ready([&](const DesktopLaunch&) { received = true; });
  if (mode == L"race") {
    const auto deadline = GetTickCount64() + 1800;
    while (!received && GetTickCount64() < deadline) {
      MsgWaitForMultipleObjects(0, nullptr, FALSE, 20, QS_ALLINPUT);
      Pump();
    }
    return received ? 100 : 92;
  }
  event = OpenEventW(EVENT_MODIFY_STATE, FALSE, EventName(key).c_str());
  if (!event) return 93;
  SetEvent(event);
  CloseHandle(event);
  // 无响应/崩溃夹具只等待父测试控制，不运行消息循环。
  Sleep(10000);
  return 0;
}

void Codec() {
  DesktopLaunch launch{123, 1, {kPayload, "--start-hidden"}};
  auto encoded = EncodeDesktopLaunch(launch);
  auto decoded = DecodeDesktopLaunch(encoded.data(), encoded.size());
  Require(decoded && decoded->id == launch.id && decoded->arguments == launch.arguments, "unicode roundtrip");
  for (size_t length = 0; length < encoded.size(); ++length) {
    Require(!DecodeDesktopLaunch(encoded.data(), length), "truncated frame accepted");
  }
  auto altered = encoded;
  altered[4] = 2;
  Require(!DecodeDesktopLaunch(altered.data(), altered.size()), "unknown version accepted");
  altered = encoded;
  altered.push_back(0);
  Require(!DecodeDesktopLaunch(altered.data(), altered.size()), "trailing data accepted");
  Require(EncodeDesktopLaunch({1, 0, {"--data-dir=其他目录"}}).empty(), "configuration switch accepted");
  Require(EncodeDesktopLaunch({1, 1, {"--activation-payload=" + std::string(33000, 'x')}}).empty(), "oversized accepted");
  Require(EncodeDesktopLaunch({1, 1, {kPayload, kPayload}}).empty(), "duplicate argument accepted");
  Require(EncodeDesktopLaunch({1, 1, {std::string("--activation-payload=\xff")}}).empty(), "invalid UTF8 accepted");
  std::puts("PASS protocol: Unicode, truncation, version, size, forbidden/duplicate arguments");
}

void WindowVisibility() {
  const auto name = L"TimeCue.D7.Visibility." + std::to_wstring(GetCurrentProcessId());
  WNDCLASSW window_class{};
  window_class.lpfnWndProc = DefWindowProcW;
  window_class.hInstance = GetModuleHandleW(nullptr);
  window_class.lpszClassName = name.c_str();
  Require(RegisterClassW(&window_class) != 0, "visibility class");
  // 测试窗口位于屏幕外，不干扰用户当前正在运行的 TimeCue。
  HWND window = CreateWindowW(name.c_str(), L"D7 visibility test", WS_OVERLAPPEDWINDOW,
      -32000, -32000, 320, 240, nullptr, nullptr, window_class.hInstance, nullptr);
  Require(window && !IsWindowVisible(window), "initial window hidden");
  Require(ShowDesktopWindow(window) && IsWindowVisible(window), "show completion not visible");
  ShowWindow(window, SW_HIDE);
  Require(!IsWindowVisible(window), "explicit hide");
  Require(ShowDesktopWindow(window) && IsWindowVisible(window), "second show not complete");
  ShowWindow(window, SW_MINIMIZE);
  Require(IsIconic(window) != FALSE, "minimize fixture");
  Require(ShowDesktopWindow(window) && !IsIconic(window), "minimized restore not complete");
  DestroyWindow(window);
  Require(!ShowDesktopWindow(window) && !ShowDesktopWindow(nullptr), "destroyed window accepted");
  UnregisterClassW(name.c_str(), window_class.hInstance);
  std::puts("PASS visibility: synchronous initial show, hide/reopen, minimize/restore, invalid window");
}
void Forwarding(const std::wstring& key) {
  SingleInstance primary(key);
  Require(primary.Start({}) == SingleInstance::Result::primary, "initial primary");
  Require(WaitChild(Spawn(L"client", key)) == 0, "pre-ready forwarding");
  int calls = 0;
  auto pending = primary.Ready([&](const DesktopLaunch& launch) {
    Require(launch.arguments == std::vector<std::string>{kPayload}, "forwarded payload changed");
    ++calls;
  });
  Require(pending.size() == 2 && pending[1].arguments[0] == kPayload, "pre-ready buffer lost");
  auto first = Spawn(L"client", key);
  auto second = Spawn(L"client", key);
  Require(WaitChild(first) == 0 && WaitChild(second) == 0 && calls == 2, "concurrent secondary forwarding");
  auto frame = EncodeDesktopLaunch({1, 1, {kPayload}});
  COPYDATASTRUCT copy{kMagic, static_cast<DWORD>(frame.size()), frame.data()};
  Require(SendMessageW(primary.handle(), WM_COPYDATA, reinterpret_cast<WPARAM>(primary.handle()),
      reinterpret_cast<LPARAM>(&copy)) == 1, "request ACK");
  Require(SendMessageW(primary.handle(), WM_COPYDATA, reinterpret_cast<WPARAM>(primary.handle()),
      reinterpret_cast<LPARAM>(&copy)) == 1 && calls == 3, "duplicate request processed");
  frame[4] = 99;
  Require(SendMessageW(primary.handle(), WM_COPYDATA, reinterpret_cast<WPARAM>(primary.handle()),
      reinterpret_cast<LPARAM>(&copy)) == 0, "malformed IPC ACK");
  primary.BeginExit();
  Require(WaitChild(Spawn(L"client", key)) == 2, "exiting accepted activation");
  std::puts("PASS IPC: pre-ready, concurrent senders, duplicate ACK, malformed reject, exiting reject");
}

void Race(const std::wstring& key) {
  HANDLE gate = CreateEventW(nullptr, TRUE, FALSE, EventName(key).c_str());
  Require(gate != nullptr, "race gate");
  auto first = Spawn(L"race", key);
  auto second = Spawn(L"race", key);
  SetEvent(gate);
  auto one = WaitChild(first), two = WaitChild(second);
  CloseHandle(gate);
  Require((one == 100 && two == 101) || (one == 101 && two == 100), "two primary processes");
  std::puts("PASS election: simultaneous startup has exactly one primary");
}

void HungAndCrash(const std::wstring& key) {
  HANDLE ready = CreateEventW(nullptr, TRUE, FALSE, EventName(key).c_str());
  Require(ready != nullptr, "hung ready event");
  auto process = Spawn(L"hung", key);
  Require(WaitForSingleObject(ready, 4000) == WAIT_OBJECT_0, "hung server ready");
  SingleInstance sender(key);
  const auto before = GetTickCount64();
  Require(sender.Start({}) == SingleInstance::Result::error, "hung server created second primary");
  const auto spent = GetTickCount64() - before;
  Require(spent >= 1700 && spent < 2700, "IPC timeout not bounded");
  // 保留发送端打开的 mutex 句柄，验证 abandoned 而非仅对象消失。
  Require(TerminateProcess(process.hProcess, 42) != FALSE, "terminate owned crash fixture");
  Require(WaitForSingleObject(process.hProcess, 2000) == WAIT_OBJECT_0, "crash fixture exit");
  CloseHandle(process.hProcess);
  CloseHandle(ready);
  SingleInstance recovered(key);
  Require(recovered.Start({}) == SingleInstance::Result::primary, "abandoned mutex not recovered");
  std::puts("PASS failure: 2s timeout without second executor, abandoned mutex recovery");
}
}  // namespace

int wmain(int argc, wchar_t** argv) {
  try {
    if (argc == 3) return Child(argv[1], argv[2]);
    const auto key = L".D7Test." + std::to_wstring(GetCurrentProcessId());
    Codec();
    WindowVisibility();
    Forwarding(key + L".forward");
    Race(key + L".race");
    HungAndCrash(key + L".hung");
    std::puts("All D7 native checks passed.");
    return 0;
  } catch (const std::exception& failure) {
    std::fprintf(stderr, "FAIL: %s\n", failure.what());
    return 1;
  }
}
