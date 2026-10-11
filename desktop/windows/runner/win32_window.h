#ifndef RUNNER_WIN32_WINDOW_H_
#define RUNNER_WIN32_WINDOW_H_

#include <windows.h>

#include <functional>
#include <memory>
#include <string>

// 封装支持高 DPI 的 Win32 窗口，供子类扩展渲染及输入处理
class Win32Window {
public:
    struct Point {
        unsigned int x;
        unsigned int y;
        // 保存窗口逻辑坐标
        // x：横坐标；y：纵坐标
        Point(unsigned int x, unsigned int y) : x(x), y(y) {}
    };

    struct Size {
        unsigned int width;
        unsigned int height;
        // 保存窗口逻辑尺寸
        // width：宽度；height：高度
        Size(unsigned int width, unsigned int height) : width(width), height(height) {}
    };

    Win32Window();
    virtual ~Win32Window();

    // 清理旧窗口，按目标显示器 DPI 缩放位置和尺寸，创建隐藏窗口并执行子类初始化
    // title：窗口标题
    // origin：逻辑起点，用于选择显示器
    // size：按 DPI 缩放前的逻辑尺寸
    // 返回 true 表示原生创建及 OnCreate 成功；false 表示其中一步失败
    bool Create(const std::wstring& title, const Point& origin, const Size& size);

    // 调用 ShowWindow 以普通状态显示窗口
    // 返回窗口此前是否可见，这是 ShowWindow 的原始结果，不代表本次显示是否成功
    bool Show();

    // 执行子类清理并销毁原生窗口；无活动窗口对象时注销窗口类
    void Destroy();

    // 将子窗口挂载到当前窗口，铺满客户区并设置焦点
    // content：借用的子窗口句柄，通常为 Flutter 视图
    void SetChildContent(HWND content);

    // 提供底层原生窗口句柄，供调用方操作窗口属性
    // 返回借用的 HWND；尚未创建或已销毁时为 nullptr
    HWND GetHandle();

    // 设置原生窗口销毁时是否向消息循环投递退出消息
    // quit_on_close：true 表示销毁时退出消息循环
    void SetQuitOnClose(bool quit_on_close);

    // 查询原生窗口客户区尺寸，供子视图布局使用
    // 返回客户区 RECT；调用方应确保窗口句柄有效，本函数不报告查询失败
    RECT GetClientArea();

protected:
    // 处理销毁、DPI、大小、焦点和主题消息，其他消息交给系统
    // hwnd：目标窗口
    // message：消息编号
    // wparam、lparam：随消息类型解释的 Win32 参数
    // 返回 Win32 消息结果；已处理消息通常返回 0
    virtual LRESULT MessageHandler(
        HWND window, UINT const message, WPARAM const wparam, LPARAM const lparam) noexcept;

    // 提供窗口创建后的子类初始化钩子；基类实现不执行额外操作
    // 基类返回 true；子类可返回 false 使 Create 失败
    virtual bool OnCreate();

    // 提供销毁前的子类资源清理钩子；基类实现为空
    virtual void OnDestroy();

private:
    friend class WindowClassRegistrar;

    // 在 WM_NCCREATE 绑定对象并启用 DPI 支持，后续消息转发给实例
    // hwnd：目标窗口
    // message：消息编号
    // wparam、lparam：随消息类型解释的 Win32 参数
    // 返回实例或 DefWindowProc 的消息处理结果
    static LRESULT CALLBACK WndProc(
        HWND const window, UINT const message, WPARAM const wparam, LPARAM const lparam) noexcept;

    // 从窗口用户数据中取回绑定的 C++ 对象
    // window：待查询的原生窗口句柄
    // 返回借用的 Win32Window 指针；未绑定时为 nullptr
    static Win32Window* GetThisFromHandle(HWND const window) noexcept;

    // 读取用户主题偏好，更新原生窗口标题栏的深色模式
    // window：待更新外观的窗口句柄
    static void UpdateTheme(HWND const window);

    bool quit_on_close_ = false;

    // 顶层原生窗口句柄
    HWND window_handle_ = nullptr;

    // 承载内容的子窗口句柄
    HWND child_content_ = nullptr;
};

#endif  // RUNNER_WIN32_WINDOW_H_
