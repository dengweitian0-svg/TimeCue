//
// Flutter 工具自动生成的文件；手工修改会在重新生成时被覆盖。
//

// 本次按目录约定格式化；重新生成会覆盖。

#include "generated_plugin_registrant.h"

#include <screen_retriever_windows/screen_retriever_windows_plugin_c_api.h>
#include <tray_manager/tray_manager_plugin.h>
#include <window_manager/window_manager_plugin.h>

void RegisterPlugins(flutter::PluginRegistry* registry) {
    ScreenRetrieverWindowsPluginCApiRegisterWithRegistrar(
        registry->GetRegistrarForPlugin("ScreenRetrieverWindowsPluginCApi"));
    TrayManagerPluginRegisterWithRegistrar(registry->GetRegistrarForPlugin("TrayManagerPlugin"));
    WindowManagerPluginRegisterWithRegistrar(registry->GetRegistrarForPlugin("WindowManagerPlugin"));
}
