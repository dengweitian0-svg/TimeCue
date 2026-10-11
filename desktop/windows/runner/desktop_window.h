#ifndef RUNNER_DESKTOP_WINDOW_H_
#define RUNNER_DESKTOP_WINDOW_H_

#include <windows.h>

// 在拥有窗口的线程同步完成显示，返回当前状态而非排队请求的回执。
inline bool ShowDesktopWindow(HWND window) {
  if (!IsWindow(window)) return false;
  if (IsIconic(window)) ShowWindow(window, SW_RESTORE);
  // 不受首次 ShowWindow 的 STARTUPINFO 隐藏标志影响，也不强行抢焦点。
  const bool applied = SetWindowPos(window, nullptr, 0, 0, 0, 0,
      SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_SHOWWINDOW) != FALSE;
  return applied && IsWindowVisible(window) != FALSE;
}

#endif
