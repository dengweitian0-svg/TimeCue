#include "managed_process.h"

#include <algorithm>
#include <utility>

namespace {
void Close(HANDLE& handle) {
  if (handle) { CloseHandle(handle); handle = nullptr; }
}
std::wstring Failure(const wchar_t* operation) {
  return std::wstring(operation) + L"（Windows 错误 " + std::to_wstring(GetLastError()) + L"）";
}
bool Pipe(HANDLE& read, HANDLE& write, bool child_reads) {
  SECURITY_ATTRIBUTES security{sizeof(SECURITY_ATTRIBUTES), nullptr, TRUE};
  if (!CreatePipe(&read, &write, &security, 0)) return false;
  return SetHandleInformation(child_reads ? write : read, HANDLE_FLAG_INHERIT, 0) != FALSE;
}
}  // namespace

ManagedProcess::ManagedProcess(std::function<void()> wake) : wake_(std::move(wake)) {}
ManagedProcess::~ManagedProcess() { Release(); }

std::wstring ManagedProcess::QuoteArgument(const std::wstring& argument) {
  std::wstring result = L"\"";
  size_t slashes = 0;
  for (wchar_t c : argument) {
    if (c == L'\\') { ++slashes; continue; }
    result.append(c == L'"' ? slashes * 2 + 1 : slashes, L'\\');
    result.push_back(c);
    slashes = 0;
  }
  result.append(slashes * 2, L'\\');
  result.push_back(L'"');
  return result;
}

bool ManagedProcess::Start(const std::wstring& executable,
    const std::vector<std::wstring>& arguments, uint64_t generation,
    DWORD& pid, std::wstring& error) {
  if (process_ || job_) { error = L"上一自有进程尚未回收。"; return false; }
  generation_ = generation;
  {
    std::lock_guard<std::mutex> lock(events_mutex_);
    events_.clear(); queued_bytes_ = 0; overflow_ = false;
  }
  HANDLE child_input = nullptr, child_output = nullptr, child_error = nullptr;
  LPPROC_THREAD_ATTRIBUTE_LIST attributes = nullptr;
  std::vector<uint8_t> storage;
  auto cleanup = [&] {
    if (attributes) DeleteProcThreadAttributeList(attributes);
    Close(child_input); Close(child_output); Close(child_error);
  };
  auto fail = [&](const wchar_t* operation) {
    error = Failure(operation);
    cleanup(); Release(); return false;
  };
  job_ = CreateJobObjectW(nullptr, nullptr);
  if (!job_) return fail(L"创建 Job 失败");
  JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
  limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
  if (!SetInformationJobObject(job_, JobObjectExtendedLimitInformation, &limits, sizeof(limits))) {
    return fail(L"配置 Job 失败");
  }
  cancel_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  if (!cancel_) return fail(L"创建取消事件失败");
  if (!Pipe(child_input, input_, true) || !Pipe(output_, child_output, false) ||
      !Pipe(error_, child_error, false)) return fail(L"创建控制管道失败");
  SIZE_T size = 0;
  InitializeProcThreadAttributeList(nullptr, 2, 0, &size);
  storage.resize(size);
  attributes = reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(storage.data());
  if (!InitializeProcThreadAttributeList(attributes, 2, 0, &size)) {
    attributes = nullptr; return fail(L"创建进程属性失败");
  }
  HANDLE inherited[] = {child_input, child_output, child_error};
  if (!UpdateProcThreadAttribute(attributes, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
      inherited, sizeof(inherited), nullptr, nullptr) ||
      !UpdateProcThreadAttribute(attributes, 0, PROC_THREAD_ATTRIBUTE_JOB_LIST,
      &job_, sizeof(job_), nullptr, nullptr)) return fail(L"绑定受控进程属性失败");
  STARTUPINFOEXW startup{};
  startup.StartupInfo.cb = sizeof(startup);
  startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
  startup.StartupInfo.hStdInput = child_input;
  startup.StartupInfo.hStdOutput = child_output;
  startup.StartupInfo.hStdError = child_error;
  startup.lpAttributeList = attributes;
  std::wstring command = QuoteArgument(executable);
  for (const auto& argument : arguments) command += L" " + QuoteArgument(argument);
  PROCESS_INFORMATION info{};
  // Job 在创建时关联，宿主在创建阶段崩溃也不会留下未监管子进程。
  if (!CreateProcessW(executable.c_str(), command.data(), nullptr, nullptr, TRUE,
      CREATE_NO_WINDOW | EXTENDED_STARTUPINFO_PRESENT, nullptr, nullptr,
      &startup.StartupInfo, &info)) return fail(L"启动 Go 失败");
  process_ = info.hProcess;
  pid = info.dwProcessId;
  CloseHandle(info.hThread);
  cleanup();
  try {
    output_thread_ = std::thread([this] { ReadPipe(output_, true); });
    error_thread_ = std::thread([this] { ReadPipe(error_, false); });
    exit_thread_ = std::thread([this] { WatchExit(); });
  } catch (...) {
    error = L"创建进程等待工作失败。"; Release(); return false;
  }
  return true;
}

void ManagedProcess::Emit(ProcessEvent event) {
  bool wake = false;
  {
    std::lock_guard<std::mutex> lock(events_mutex_);
    if (WaitForSingleObject(cancel_, 0) == WAIT_OBJECT_0) return;
    // stderr 的积压可以截断；stdout 超限必须报告协议失败，不能丢掉再冒充有效握手。
    if (event.type == "stderr" && queued_bytes_ + event.bytes.size() > 8192) return;
    if (queued_bytes_ + event.bytes.size() > 65536) {
      if (event.type == "stderr") return;
      if (overflow_) return;
      overflow_ = true;
      event.type = "error";
      event.bytes.clear();
    }
    wake = events_.empty();
    queued_bytes_ += event.bytes.size();
    events_.push_back(std::move(event));
  }
  if (wake && wake_) wake_();
}

void ManagedProcess::ReadPipe(HANDLE pipe, bool output) {
  while (WaitForSingleObject(cancel_, 0) == WAIT_TIMEOUT) {
    DWORD available = 0;
    if (!PeekNamedPipe(pipe, nullptr, 0, nullptr, &available, nullptr)) {
      Emit({generation_, output ? "stdoutEof" : "stderrEof", {}, 0});
      return;
    }
    if (!available) {
      // 本地管道取消等待，不是服务健康轮询或执行者心跳。
      if (WaitForSingleObject(cancel_, 10) != WAIT_TIMEOUT) return;
      continue;
    }
    std::vector<uint8_t> bytes(std::min<DWORD>(available, 4096));
    DWORD count = 0;
    if (!ReadFile(pipe, bytes.data(), static_cast<DWORD>(bytes.size()), &count, nullptr)) {
      Emit({generation_, output ? "stdoutEof" : "stderrEof", {}, 0});
      return;
    }
    bytes.resize(count);
    Emit({generation_, output ? "stdout" : "stderr", std::move(bytes), 0});
  }
}

void ManagedProcess::WatchExit() {
  HANDLE handles[] = {cancel_, process_};
  if (WaitForMultipleObjects(2, handles, FALSE, INFINITE) != WAIT_OBJECT_0 + 1) return;
  DWORD code = 0;
  GetExitCodeProcess(process_, &code);
  Emit({generation_, "exit", {}, code});
}

void ManagedProcess::Shutdown() {
  if (!input_) return;
  const char command[] = "{\"command\":\"shutdown\"}\n";
  DWORD written = 0;
  // 控制管道只写这一条小消息；失败或部分写入之后也关闭父端，由 EOF 取消 Go。
  WriteFile(input_, command, static_cast<DWORD>(sizeof(command) - 1), &written, nullptr);
  Close(input_);
}
bool ManagedProcess::Alive() const {
  return process_ && WaitForSingleObject(process_, 0) == WAIT_TIMEOUT;
}
bool ManagedProcess::Wait(DWORD milliseconds) {
  return !process_ || WaitForSingleObject(process_, milliseconds) == WAIT_OBJECT_0;
}
std::vector<ProcessEvent> ManagedProcess::TakeEvents() {
  std::lock_guard<std::mutex> lock(events_mutex_);
  std::vector<ProcessEvent> result;
  while (!events_.empty()) {
    result.push_back(std::move(events_.front())); events_.pop_front();
  }
  queued_bytes_ = 0;
  return result;
}
void ManagedProcess::Release() {
  // 先触发内核回收，再解除管道和进程等待；即使优雅预算为零也执行。
  if (cancel_) SetEvent(cancel_);
  Close(job_);
  if (output_thread_.joinable()) output_thread_.join();
  if (error_thread_.joinable()) error_thread_.join();
  if (exit_thread_.joinable()) exit_thread_.join();
  Close(input_); Close(output_); Close(error_); Close(process_); Close(cancel_);
}
