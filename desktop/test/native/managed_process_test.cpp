#include "managed_process.h"
#include "launch_configuration.h"
#include <windows.h>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>

namespace {
void Check(bool value, const char* message) { if (!value) throw std::runtime_error(message); }
std::wstring Self() {
  std::wstring path(32768, L'\0');
  const DWORD size = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
  path.resize(size); return path;
}
void Write(HANDLE output, const std::string& text) {
  DWORD count = 0; WriteFile(output, text.data(), static_cast<DWORD>(text.size()), &count, nullptr);
}
int Child(int argc, wchar_t** argv) {
  Check(GetConsoleWindow() == nullptr, "child must not have a console window");
  const auto mode = argc > 2 ? std::wstring(argv[2]) : L"normal";
  if (mode == L"leaf") { Sleep(INFINITE); return 0; }
  if (mode == L"arguments") {
    // 把解码后的真实 Unicode 参数原样回传，验证 Windows 命令行编码。
    for (int i = 3; i < argc; ++i) {
      const std::wstring argument(argv[i]);
      const int size = WideCharToMultiByte(CP_UTF8, 0, argument.data(), static_cast<int>(argument.size()),
          nullptr, 0, nullptr, nullptr);
      std::string text(size, '\0');
      WideCharToMultiByte(CP_UTF8, 0, argument.data(), static_cast<int>(argument.size()),
          text.data(), size, nullptr, nullptr);
      Write(GetStdHandle(STD_OUTPUT_HANDLE), text + "\n");
    }
    return 0;
  }
  if (mode == L"descendant") {
    STARTUPINFOW startup{}; startup.cb = sizeof(startup);
    PROCESS_INFORMATION info{};
    std::wstring command = ManagedProcess::QuoteArgument(Self()) + L" --child leaf";
    Check(CreateProcessW(Self().c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
        nullptr, nullptr, &startup, &info) != FALSE, "spawn descendant");
    std::ofstream file{std::filesystem::path(argv[3])}; file << info.dwProcessId; file.close();
    CloseHandle(info.hThread); CloseHandle(info.hProcess);
  }
  if (mode == L"stderr") Write(GetStdHandle(STD_ERROR_HANDLE), std::string(1024 * 1024, 'x'));
  Write(GetStdHandle(STD_OUTPUT_HANDLE), "ready\n");
  if (mode == L"hang" || mode == L"descendant") { Sleep(INFINITE); return 0; }
  char buffer[1024]{}; DWORD count = 0;
  ReadFile(GetStdHandle(STD_INPUT_HANDLE), buffer, sizeof(buffer), &count, nullptr);
  return std::string(buffer, count) == "{\"command\":\"shutdown\"}\n" ? 0 : 4;
}
DWORD ReadPid(const std::filesystem::path& path) {
  const auto deadline = GetTickCount64() + 5000;
  DWORD pid = 0;
  while (!pid && GetTickCount64() < deadline) {
    std::ifstream file(path); file >> pid;
    if (!pid) Sleep(10);
  }
  Check(pid != 0, "pid file"); return pid;
}
HANDLE Observe(DWORD pid) {
  HANDLE handle = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
  Check(handle != nullptr, "observe own child"); return handle;
}
int GoDriver(const wchar_t* executable, const wchar_t* directory, const wchar_t* identity) {
  ManagedProcess process;
  DWORD pid = 0; std::wstring failure;
  Check(process.Start(executable, {L"--data-dir", directory, L"--instance-id", identity},
      1, pid, failure), "real Go creation failed");
  bool line = false;
  const auto deadline = GetTickCount64() + 10000;
  while (!line && process.Alive() && GetTickCount64() < deadline) {
    for (const auto& event : process.TakeEvents()) {
      const std::string bytes(event.bytes.begin(), event.bytes.end());
      if (event.type == "stdout") {
        std::cout << bytes << std::flush;
        line = line || bytes.find('\n') != std::string::npos;
      } else if (event.type == "stderr") {
        std::cerr << bytes << std::flush;
      }
    }
    Sleep(5);
  }
  Check(line, "real Go ready missing");
  std::string command;
  std::getline(std::cin, command);
  process.Shutdown();
  Check(process.Wait(5000), "real Go shutdown timeout");
  process.Release();
  return 0;
}
void Run() {
  DWORD pid = 0; std::wstring error;
  ManagedProcess process;
  Check(!process.Start(L"Z:\\missing-timecue.exe", {}, 1, pid, error), "missing exe must fail");
  Check(!process.Alive(), "failed start cleanup");
  Check(process.Start(Self(), {L"--child", L"normal"}, 2, pid, error), "normal start");
  Check(process.Alive(), "alive");
  Check(!process.Start(Self(), {}, 3, pid, error), "no second live child");
  process.Shutdown();
  Check(process.Wait(5000), "shutdown complete");
  process.Release(); process.Release();
  Check(process.Start(Self(), {L"--child", L"stderr"}, 3, pid, error), "stderr start");
  const auto deadline = GetTickCount64() + 5000;
  bool ready = false;
  while (!ready && GetTickCount64() < deadline) {
    for (const auto& event : process.TakeEvents()) {
      if (event.type == "stdout") ready = true;
    }
    Sleep(10);
  }
  Check(ready, "large stderr must not block stdout");
  process.Shutdown(); Check(process.Wait(5000), "stderr shutdown"); process.Release();
  Check(process.Start(Self(), {L"--child", L"arguments", L"中文 路径", L"a\"b", L"尾部\\", L""}, 4, pid, error),
      "unicode arguments");
  Check(process.Wait(5000), "arguments exit");
  Sleep(30);
  std::string output;
  for (const auto& event : process.TakeEvents()) {
    if (event.type == "stdout") output.append(event.bytes.begin(), event.bytes.end());
  }
  Check(output == u8"中文 路径\na\"b\n尾部\\\n\n", "argument boundaries");
  process.Release();
  const auto path = std::filesystem::temp_directory_path() /
      (L"timecue-d8-" + std::to_wstring(GetCurrentProcessId()) + L".pid");
  Check(process.Start(Self(), {L"--child", L"descendant", path.wstring()}, 5, pid, error), "tree start");
  const HANDLE child = Observe(pid);
  const HANDLE descendant = Observe(ReadPid(path));
  process.Release();
  Check(WaitForSingleObject(child, 5000) == WAIT_OBJECT_0, "job child cleanup");
  Check(WaitForSingleObject(descendant, 5000) == WAIT_OBJECT_0, "job descendant cleanup");
  CloseHandle(child); CloseHandle(descendant); std::filesystem::remove(path);

  // 外层 Job 模拟 CI/IDE；自有进程必须允许嵌套监管。
  HANDLE outer = CreateJobObjectW(nullptr, nullptr);
  Check(AssignProcessToJobObject(outer, GetCurrentProcess()) != FALSE, "outer job");
  Check(process.Start(Self(), {L"--child", L"normal"}, 6, pid, error), "nested job start");
  process.Shutdown(); Check(process.Wait(5000), "nested shutdown"); process.Release(); CloseHandle(outer);

  const auto config = ParseLaunchConfiguration({"--backend=managed", "--data-dir=.", "--go-executable=C:/中文 路径/go.exe"});
  Check(config && config->backend == "managed" && !config->data_dir.empty(), "config parse");
  Check(!ParseLaunchConfiguration({"--backend=unknown"}), "bad mode");
  Check(!ParseLaunchConfiguration({"--data-dir="}), "empty directory");
  Check(!ParseLaunchConfiguration({"--data-dir=.", "--data-dir=."}), "duplicate directory");
  Check(!ParseLaunchConfiguration({"--go-executable=relative.exe"}), "relative executable");
  Check(CompatibleLaunchConfiguration(*config, *ParseLaunchConfiguration({})), "omitted options reuse");
  Check(CompatibleLaunchConfiguration(*config, *ParseLaunchConfiguration({"--go-executable=c:/中文 路径/GO.EXE"})), "case insensitive path");
  Check(CompatibleLaunchConfiguration(*config, *ParseLaunchConfiguration({"--data-dir=./"})), "normalized trailing slash");
  Check(!CompatibleLaunchConfiguration(*config, *ParseLaunchConfiguration({"--backend=external"})), "mode conflict");
  Check(!CompatibleLaunchConfiguration(*config, *ParseLaunchConfiguration({"--data-dir=C:/other"})), "directory conflict");

  // 宿主崩溃测试：只终止根测试明确创建并持有句柄的 owner。
  STARTUPINFOW startup{}; startup.cb = sizeof(startup);
  PROCESS_INFORMATION owner{};
  std::wstring command = ManagedProcess::QuoteArgument(Self()) + L" --owner " + ManagedProcess::QuoteArgument(path.wstring());
  Check(CreateProcessW(Self().c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
      nullptr, nullptr, &startup, &owner) != FALSE, "owner start");
  const HANDLE owned = Observe(ReadPid(path));
  Check(TerminateProcess(owner.hProcess, 99) != FALSE, "owner crash");
  Check(WaitForSingleObject(owned, 5000) == WAIT_OBJECT_0, "crash job cleanup");
  WaitForSingleObject(owner.hProcess, 5000);
  CloseHandle(owned); CloseHandle(owner.hThread); CloseHandle(owner.hProcess);
  std::filesystem::remove(path);
}
}  // namespace
int wmain(int argc, wchar_t** argv) {
  try {
    if (argc == 5 && std::wstring(argv[1]) == L"--go") return GoDriver(argv[2], argv[3], argv[4]);
    if (argc > 1 && std::wstring(argv[1]) == L"--child") return Child(argc, argv);
    if (argc > 2 && std::wstring(argv[1]) == L"--owner") {
      ManagedProcess process; DWORD pid = 0; std::wstring error;
      Check(process.Start(Self(), {L"--child", L"hang"}, 1, pid, error), "owner child");
      std::ofstream file{std::filesystem::path(argv[2])}; file << pid; file.close();
      Sleep(INFINITE); return 0;
    }
    Run(); std::cout << "D8 native process and configuration tests passed\n"; return 0;
  } catch (const std::exception& error) {
    std::cerr << error.what() << "\n"; return 1;
  }
}
