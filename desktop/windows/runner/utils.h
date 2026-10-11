#ifndef RUNNER_UTILS_H_
#define RUNNER_UTILS_H_

#include <string>
#include <vector>

// 创建调试控制台，重定向标准输出与错误并同步 Flutter 输出流
void CreateAndAttachConsole();

// 有界读取 UTF-16 文本并转换成 Flutter 使用的 UTF-8
// utf16_string：以空字符结束的 UTF-16 指针，可为 nullptr；读取上限为 UNICODE_STRING_MAX_CHARS
// 返回 UTF-8 字符串；空指针、空串或转换失败时返回空串
std::string Utf8FromUtf16(const wchar_t* utf16_string);

// 读取 Windows 命令行并去掉程序名，将每个参数转换为 UTF-8
// 返回参数列表；命令行解析失败时返回空列表，单个转换失败的参数为空串。
std::vector<std::string> GetCommandLineArguments();

#endif  // 头文件保护结束：RUNNER_UTILS_H_
