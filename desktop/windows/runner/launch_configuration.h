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

// 解析启动选项并规范化路径，记录哪些选项由调用方显式指定。
// arguments：不含程序名的 UTF-8 参数列表。
// 返回启动配置；重复选项、未知选项或无效路径返回 std::nullopt；首次启动完整性由 Dart 核验。
std::optional<LaunchConfiguration> ParseLaunchConfiguration(
    const std::vector<std::string>& arguments);

// 为主实例补全发行默认值；显式 external 仍要求数据目录。
// configuration：已解析的参数，只补全未显式指定的值。
// 返回 false 表示用户目录或程序目录解析失败。
bool ResolveLaunchDefaults(LaunchConfiguration& configuration);

// 检查二次启动的显式选项是否与主实例配置兼容。
// current：主实例配置
// requested：二次启动配置，省略的选项复用主实例值。
// 返回 true 表示兼容；路径比较忽略大小写。
bool CompatibleLaunchConfiguration(
    const LaunchConfiguration& current, const LaunchConfiguration& requested);

// 将启动参数由 UTF-8 转成 Windows 使用的 UTF-16。
// value：待转换的 UTF-8 文本。
// 返回宽字符串；空输入或转换失败时返回空串。
std::wstring LaunchWide(const std::string& value);
#endif
