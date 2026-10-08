import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import '../contracts/notebook_repository.dart';
import '../features/room/room_screen.dart';
import '../theme/notebook_theme.dart';

class TimeCueApp extends StatelessWidget {
  const TimeCueApp({super.key, required this.repository});
  final NotebookRepository repository;
  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'TimeCue',
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN')],
    // 保持 stable SDK 的官方本地化入口；本机 master 正在迁移 Material 包。
    // ignore: deprecated_member_use
    localizationsDelegates: GlobalMaterialLocalizations.delegates,
    debugShowCheckedModeBanner: false,
    theme: NotebookTheme.data,
    home: DesktopShell(repository: repository),
  );
}

/// 阶段 B 装配房间与 Go 查询仓储；托盘、通知和子进程留给运行时适配阶段。
class DesktopShell extends StatelessWidget {
  const DesktopShell({super.key, required this.repository});
  final NotebookRepository repository;
  @override
  Widget build(BuildContext context) =>
      Scaffold(body: RoomScreen(repository: repository));
}
