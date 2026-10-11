#ifndef RUNNER_LAUNCH_CONFIGURATION_H_
#define RUNNER_LAUNCH_CONFIGURATION_H_
#include <optional>
#include <string>
#include <vector>

struct LaunchConfiguration {
  std::string backend = "external";
  std::string data_dir;
  std::string executable;
  bool backend_explicit = false, data_explicit = false, executable_explicit = false;
};
std::optional<LaunchConfiguration> ParseLaunchConfiguration(const std::vector<std::string>& arguments);
bool CompatibleLaunchConfiguration(const LaunchConfiguration& current, const LaunchConfiguration& requested);
std::wstring LaunchWide(const std::string& value);
#endif
