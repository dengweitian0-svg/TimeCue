#include "launch_configuration.h"
#include <windows.h>
#include <filesystem>

std::wstring LaunchWide(const std::string& value) {
  const int size = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), nullptr, 0);
  if (!size) return L"";
  std::wstring result(size, L'\0');
  MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), result.data(), size);
  return result;
}
namespace {
std::string Utf8(const std::wstring& value) {
  const int size = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
  if (!size) return "";
  std::string result(size, '\0');
  WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), result.data(), size, nullptr, nullptr);
  return result;
}
std::string Absolute(const std::string& value, bool require_absolute) {
  const auto wide = LaunchWide(value);
  if (wide.empty() || (require_absolute && !std::filesystem::path(wide).is_absolute())) return "";
  DWORD size = GetFullPathNameW(wide.c_str(), 0, nullptr, nullptr);
  if (!size || size > 32768) return "";
  std::wstring full(size, L'\0');
  const DWORD count = GetFullPathNameW(wide.c_str(), size, full.data(), nullptr);
  if (!count || count >= size) return "";
  full.resize(count);
  const auto normalized = std::filesystem::path(full).lexically_normal();
  full = normalized.wstring();
  const auto root_size = normalized.root_path().wstring().size();
  while (full.size() > root_size && (full.back() == L'\\' || full.back() == L'/')) full.pop_back();
  return Utf8(full);
}
bool SamePath(const std::string& a, const std::string& b) {
  const auto left = LaunchWide(a), right = LaunchWide(b);
  return CompareStringOrdinal(left.c_str(), static_cast<int>(left.size()),
      right.c_str(), static_cast<int>(right.size()), TRUE) == CSTR_EQUAL;
}
}  // namespace

std::optional<LaunchConfiguration> ParseLaunchConfiguration(const std::vector<std::string>& arguments) {
  LaunchConfiguration result;
  for (const auto& argument : arguments) {
    if (argument.rfind("--backend=", 0) == 0) {
      if (result.backend_explicit) return std::nullopt;
      result.backend_explicit = true;
      result.backend = argument.substr(10);
      if (result.backend != "managed" && result.backend != "external") return std::nullopt;
    } else if (argument.rfind("--data-dir=", 0) == 0) {
      if (result.data_explicit) return std::nullopt;
      result.data_explicit = true;
      result.data_dir = Absolute(argument.substr(11), false);
      if (result.data_dir.empty()) return std::nullopt;
    } else if (argument.rfind("--go-executable=", 0) == 0) {
      if (result.executable_explicit) return std::nullopt;
      result.executable_explicit = true;
      result.executable = Absolute(argument.substr(16), true);
      if (result.executable.empty()) return std::nullopt;
    } else if (argument != "--login-start" && argument != "--start-hidden" &&
        argument.rfind("--activation-payload=", 0) != 0) {
      return std::nullopt;
    }
  }
  // 完整性在首次 Dart 装配核验；转交可以只声明一个待核对的选项。
  return result;
}
bool CompatibleLaunchConfiguration(const LaunchConfiguration& current, const LaunchConfiguration& requested) {
  return (!requested.backend_explicit || requested.backend == current.backend) &&
      (!requested.data_explicit || SamePath(requested.data_dir, current.data_dir)) &&
      (!requested.executable_explicit || SamePath(requested.executable, current.executable));
}
