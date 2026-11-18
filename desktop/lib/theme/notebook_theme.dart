import 'package:flutter/material.dart';

abstract final class NotebookTheme {
  static const paper = Color.fromARGB(255, 251, 246, 231);
  static const ink = Color.fromARGB(255, 89, 71, 51);
  static const muted = Color.fromARGB(255, 139, 121, 100);
  static const sage = Color.fromARGB(255, 100, 119, 80);
  static const line = Color.fromARGB(255, 222, 212, 188);
  static const cover = Color.fromARGB(255, 145, 116, 79);

  /// 供纸面主操作使用的深棕色，确保小号按钮文字达到可读对比度。
  static const action = Color.fromARGB(255, 96, 68, 40);

  /// 房间入口与猫咪提示共用的半透明暖棕背景。
  static const roomActionBackground = Color.fromARGB(128, 73, 61, 48);

  /// 房间入口与猫咪提示共用的半透明米白前景。
  static const roomActionForeground = Color.fromARGB(159, 255, 249, 237);

  /// 封面上的小号文字使用纸色标签承载，避免改变封面底色。
  static const coverLabelBackground = paper;

  /// 封面纸色标签上的文字颜色。
  static const coverLabelForeground = ink;

  /// 房间固定入口共用半透明暖棕胶囊样式，并保留添加入口的禁用状态。
  static ButtonStyle roomActionStyle({bool disabledCreate = false}) =>
      FilledButton.styleFrom(
        backgroundColor: roomActionBackground,
        foregroundColor: roomActionForeground,
        side: BorderSide.none,
        disabledBackgroundColor: disabledCreate
            ? const Color.fromARGB(225, 238, 222, 190)
            : const Color.fromARGB(255, 242, 235, 218),
        disabledForegroundColor: disabledCreate
            ? const Color.fromARGB(255, 93, 66, 43)
            : ink,
        minimumSize: const Size(0, 44),
        padding: const EdgeInsets.symmetric(horizontal: 14),
        shape: const StadiumBorder(side: BorderSide.none),
        textStyle: const TextStyle(fontSize: 12),
      );

  static ThemeData get data => ThemeData(
    useMaterial3: true,
    fontFamily: 'Microsoft YaHei',
    fontFamilyFallback: const ['Segoe UI', 'SimSun'],
    colorScheme: ColorScheme.fromSeed(
      seedColor: cover,
      primary: action,
      onPrimary: paper,
      primaryContainer: line,
      onPrimaryContainer: ink,
      secondary: action,
      onSecondary: paper,
      secondaryContainer: line,
      onSecondaryContainer: ink,
      surface: paper,
      onSurface: ink,
      surfaceContainerHighest: const Color.fromARGB(255, 242, 235, 218),
      outline: muted,
      outlineVariant: line,
    ),
    scaffoldBackgroundColor: paper,
    dialogTheme: const DialogThemeData(
      backgroundColor: paper,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(7)),
        side: BorderSide(color: cover, width: 5),
      ),
    ),
    // 日期弹层独立于普通 Dialog 主题，显式保持米色纸面与暖棕选中态。
    datePickerTheme: const DatePickerThemeData(
      backgroundColor: paper,
      surfaceTintColor: Colors.transparent,
      headerBackgroundColor: paper,
      headerForegroundColor: ink,
      dividerColor: line,
      todayBorder: BorderSide(color: cover),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(7)),
        side: BorderSide(color: cover, width: 5),
      ),
    ),
    textSelectionTheme: const TextSelectionThemeData(
      cursorColor: cover,
      selectionColor: Color.fromARGB(64, 145, 116, 79),
      selectionHandleColor: cover,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: action,
        foregroundColor: paper,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(foregroundColor: ink),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: ink,
        side: const BorderSide(color: muted),
      ),
    ),
    textTheme: const TextTheme(
      bodyMedium: TextStyle(fontSize: 14, color: ink),
      titleLarge: TextStyle(fontFamily: 'SimSun', fontSize: 28, color: ink),
    ),
    inputDecorationTheme: const InputDecorationTheme(
      border: OutlineInputBorder(),
      enabledBorder: OutlineInputBorder(borderSide: BorderSide(color: muted)),
      focusedBorder: OutlineInputBorder(
        borderSide: BorderSide(color: cover, width: 2),
      ),
      labelStyle: TextStyle(color: ink),
      floatingLabelStyle: TextStyle(color: ink),
      contentPadding: EdgeInsets.all(12),
    ),
    dividerColor: line,
  );
}

class PaperPage extends StatelessWidget {
  const PaperPage({super.key, required this.child, this.right = false});
  final Widget child;
  final bool right;
  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      gradient: LinearGradient(
        colors: right
            ? const [
                Color.fromARGB(255, 229, 220, 198),
                NotebookTheme.paper,
                Color.fromARGB(255, 255, 250, 240),
              ]
            : const [
                Color.fromARGB(255, 255, 250, 240),
                NotebookTheme.paper,
                Color.fromARGB(255, 229, 220, 198),
              ],
        stops: right ? const [0, .055, 1] : const [0, .945, 1],
      ),
    ),
    child: Material(
      type: MaterialType.transparency,
      child: SingleChildScrollView(
        padding: const EdgeInsets.fromLTRB(30, 34, 30, 30),
        child: child,
      ),
    ),
  );
}

class PageHeading extends StatelessWidget {
  const PageHeading(
    this.title,
    this.subtitle, {
    super.key,
    this.eyebrow = 'A LITTLE EVERYDAY',
  });
  final String title, subtitle, eyebrow;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        eyebrow,
        style: const TextStyle(
          fontSize: 10,
          letterSpacing: 2.4,
          color: NotebookTheme.muted,
        ),
      ),
      const SizedBox(height: 12),
      Text(title, style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 8),
      Text(
        subtitle,
        style: const TextStyle(fontSize: 12, color: NotebookTheme.muted),
      ),
      const SizedBox(height: 24),
      const Divider(),
      const SizedBox(height: 12),
    ],
  );
}
