#ifndef RUNNER_DESKTOP_WINDOW_H_
#define RUNNER_DESKTOP_WINDOW_H_

#include <windows.h>

// 在窗口所属线程同步恢复并显示窗口，核验最终可见性。
// window：要显示的窗口句柄，调用方必须在所属线程调用。
// 返回 true 表示位置设置成功且窗口可见；无效窗口或显示失败返回 false。
inline bool ShowDesktopWindow(HWND window) {
    if (!IsWindow(window)) return false;
    if (IsIconic(window)) ShowWindow(window, SW_RESTORE);
    // 不受首次 ShowWindow 的 STARTUPINFO 隐藏标志影响，也不强行抢焦点。
    const bool applied =
        SetWindowPos(
            window,
            nullptr,
            0,
            0,
            0,
            0,
            SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_SHOWWINDOW) != FALSE;
    return applied && IsWindowVisible(window) != FALSE;
}

#endif
