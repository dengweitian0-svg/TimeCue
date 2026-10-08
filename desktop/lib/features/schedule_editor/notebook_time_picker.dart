import 'dart:math' as math;
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../theme/notebook_theme.dart';

Future<TimeOfDay?> showNotebookTimePicker(
  BuildContext context,
  TimeOfDay initialTime,
) => showDialog<TimeOfDay>(
  context: context,
  builder: (_) => NotebookTimePicker(initialTime: initialTime),
);

class NotebookTimePicker extends StatefulWidget {
  const NotebookTimePicker({super.key, required this.initialTime});
  final TimeOfDay initialTime;
  @override
  State<NotebookTimePicker> createState() => _NotebookTimePickerState();
}

class _NotebookTimePickerState extends State<NotebookTimePicker> {
  late TimeOfDay time = widget.initialTime;
  bool hours = true;
  String? error;
  late final hourInput = TextEditingController(
    text: widget.initialTime.hour.toString().padLeft(2, '0'),
  );
  late final minuteInput = TextEditingController(
    text: widget.initialTime.minute.toString().padLeft(2, '0'),
  );
  final hourFocus = FocusNode();
  final minuteFocus = FocusNode();

  @override
  void initState() {
    super.initState();
    hourFocus.addListener(() => focusField(true));
    minuteFocus.addListener(() => focusField(false));
  }

  void focusField(bool hour) {
    final focus = hour ? hourFocus : minuteFocus;
    if (!focus.hasFocus) return;
    final controller = hour ? hourInput : minuteInput;
    controller.selection = TextSelection(
      baseOffset: 0,
      extentOffset: controller.text.length,
    );
    setState(() => hours = hour);
  }

  bool readInput() {
    final hour = int.tryParse(hourInput.text);
    final minute = int.tryParse(minuteInput.text);
    if (hour == null ||
        hour < 0 ||
        hour > 23 ||
        minute == null ||
        minute < 0 ||
        minute > 59) {
      setState(() => error = '小时请输入 0–23，分钟请输入 0–59');
      return false;
    }
    time = TimeOfDay(hour: hour, minute: minute);
    error = null;
    return true;
  }

  void edit(bool hour, String text) {
    final value = int.tryParse(text);
    setState(() {
      hours = hour;
      if (value != null && value >= 0 && value < (hour ? 24 : 60)) {
        time = hour
            ? time.replacing(hour: value)
            : time.replacing(minute: value);
        error = null;
      } else {
        // 空字段允许继续输入；越界数字保留草稿，不能确认成上次的有效值。
        error = text.isEmpty ? null : '小时请输入 0–23，分钟请输入 0–59';
      }
    });
    // 只在用户输满两位合法小时后前进，转盘回填不会触发焦点跳转。
    if (hour && text.length == 2 && value != null && value >= 0 && value < 24) {
      minuteFocus.requestFocus();
    }
  }

  void choose(int value) {
    final controller = hours ? hourInput : minuteInput;
    controller.text = value.toString().padLeft(2, '0');
    setState(() {
      time = hours
          ? time.replacing(hour: value)
          : time.replacing(minute: value);
      error = null;
    });
  }

  void step(int delta) =>
      choose(((hours ? time.hour : time.minute) + delta) % (hours ? 24 : 60));
  void submit() {
    if (readInput()) Navigator.pop(context, time);
  }

  @override
  void dispose() {
    hourFocus.dispose();
    minuteFocus.dispose();
    hourInput.dispose();
    minuteInput.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => CallbackShortcuts(
    bindings: {
      const SingleActivator(LogicalKeyboardKey.arrowRight): () => step(1),
      const SingleActivator(LogicalKeyboardKey.arrowUp): () => step(1),
      const SingleActivator(LogicalKeyboardKey.arrowLeft): () => step(-1),
      const SingleActivator(LogicalKeyboardKey.arrowDown): () => step(-1),
    },
    child: Dialog(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 620),
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('选择时间', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 8),
              const Text(
                '北京时间 · 可直接输入，或拖动指针、滚轮逐格调整',
                style: TextStyle(fontSize: 12, color: NotebookTheme.muted),
              ),
              const SizedBox(height: 24),
              LayoutBuilder(
                builder: (context, constraints) {
                  final header = Wrap(
                    alignment: WrapAlignment.center,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      _TimeField(
                        controller: hourInput,
                        focusNode: hourFocus,
                        label: '小时',
                        active: hours,
                        onChanged: (text) => edit(true, text),
                        onSubmitted: () => minuteFocus.requestFocus(),
                      ),
                      const Padding(
                        padding: EdgeInsets.symmetric(horizontal: 8),
                        child: Text(
                          ':',
                          style: TextStyle(
                            fontSize: 38,
                            color: NotebookTheme.muted,
                          ),
                        ),
                      ),
                      _TimeField(
                        controller: minuteInput,
                        focusNode: minuteFocus,
                        label: '分钟',
                        active: !hours,
                        onChanged: (text) => edit(false, text),
                        onSubmitted: submit,
                      ),
                    ],
                  );
                  final dial = NotebookTimeDial(
                    hours: hours,
                    value: hours ? time.hour : time.minute,
                    onChanged: choose,
                    onStep: step,
                  );
                  if (constraints.maxWidth >= 530 &&
                      MediaQuery.textScalerOf(context).scale(14) < 20) {
                    return Row(
                      children: [
                        header,
                        const SizedBox(width: 24),
                        Expanded(child: dial),
                      ],
                    );
                  }
                  return Column(
                    children: [
                      Center(child: header),
                      const SizedBox(height: 20),
                      dial,
                    ],
                  );
                },
              ),
              const SizedBox(height: 16),
              Row(
                children: [
                  IconButton(
                    onPressed: () => step(-1),
                    tooltip: hours ? '上一小时' : '上一分钟',
                    icon: const Icon(Icons.remove),
                  ),
                  Flexible(
                    child: Text(
                      hours ? '每格 1 小时 · 30°' : '每格 1 分钟 · 6°',
                      style: const TextStyle(
                        fontSize: 12,
                        color: NotebookTheme.muted,
                      ),
                    ),
                  ),
                  IconButton(
                    onPressed: () => step(1),
                    tooltip: hours ? '下一小时' : '下一分钟',
                    icon: const Icon(Icons.add),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              if (error != null)
                Text(
                  error!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: () => Navigator.pop(context),
                    child: const Text('取消'),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    style: FilledButton.styleFrom(
                      backgroundColor: NotebookTheme.cover,
                      foregroundColor: NotebookTheme.paper,
                    ),
                    onPressed: submit,
                    child: const Text('确定'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _TimeField extends StatelessWidget {
  const _TimeField({
    required this.controller,
    required this.focusNode,
    required this.label,
    required this.active,
    required this.onChanged,
    required this.onSubmitted,
  });
  final TextEditingController controller;
  final FocusNode focusNode;
  final String label;
  final bool active;
  final ValueChanged<String> onChanged;
  final VoidCallback onSubmitted;
  @override
  Widget build(BuildContext context) => Semantics(
    selected: active,
    child: Container(
      width: 96,
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 10),
      decoration: BoxDecoration(
        color: active
            ? NotebookTheme.line
            : const Color.fromARGB(255, 242, 235, 218),
        borderRadius: BorderRadius.circular(6),
      ),
      child: TextSelectionTheme(
        data: const TextSelectionThemeData(
          cursorColor: NotebookTheme.cover,
          selectionColor: Color.fromARGB(64, 145, 116, 79),
          selectionHandleColor: NotebookTheme.cover,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              key: ValueKey('time-field-$label'),
              controller: controller,
              focusNode: focusNode,
              onChanged: onChanged,
              onSubmitted: (_) => onSubmitted(),
              keyboardType: TextInputType.number,
              textInputAction: label == '小时'
                  ? TextInputAction.next
                  : TextInputAction.done,
              inputFormatters: [
                FilteringTextInputFormatter.digitsOnly,
                LengthLimitingTextInputFormatter(2),
              ],
              textAlign: TextAlign.center,
              cursorColor: NotebookTheme.cover,
              style: TextStyle(
                fontSize: 42,
                fontFamily: 'Microsoft YaHei',
                color: active ? NotebookTheme.ink : NotebookTheme.muted,
              ),
              decoration: InputDecoration(
                isDense: true,
                contentPadding: EdgeInsets.zero,
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                labelText: null,
                hintText: '00',
                hintStyle: const TextStyle(color: NotebookTheme.muted),
                semanticCounterText: label,
              ),
            ),
            Text(
              label,
              style: const TextStyle(fontSize: 11, color: NotebookTheme.muted),
            ),
          ],
        ),
      ),
    ),
  );
}

/// 小时按 12 个角度、内外双圈定位，分钟按 60 个角度定位，指针始终吸附刻度。
class NotebookTimeDial extends StatelessWidget {
  const NotebookTimeDial({
    super.key,
    required this.hours,
    required this.value,
    required this.onChanged,
    required this.onStep,
  });
  final bool hours;
  final int value;
  final ValueChanged<int> onChanged, onStep;
  int get divisions => hours ? 12 : 60;
  int snap(Offset point, double diameter) {
    final delta = point - Offset(diameter / 2, diameter / 2);
    if (delta.distance < diameter * .08) return value;
    final angle =
        (math.atan2(delta.dy, delta.dx) + math.pi / 2) % (2 * math.pi);
    final tick = (angle / (2 * math.pi) * divisions).round() % divisions;
    return hours && delta.distance < diameter * .32 ? tick + 12 : tick;
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final diameter = math.min(300.0, constraints.maxWidth);
      final labels = hours
          ? List.generate(24, (i) => i)
          : [
              // 暂隐紧邻当前分钟的数字，保留刻度，避免两个标签挤在一起。
              for (var i = 0; i < 60; i += 5)
                if (value % 5 == 0 ||
                    math.min((i - value).abs(), 60 - (i - value).abs()) >= 2)
                  i,
              if (value % 5 != 0) value,
            ];
      return Center(
        child: SizedBox(
          width: diameter,
          height: diameter,
          child: CallbackShortcuts(
            bindings: {
              const SingleActivator(LogicalKeyboardKey.arrowRight): () =>
                  onStep(1),
              const SingleActivator(LogicalKeyboardKey.arrowUp): () =>
                  onStep(1),
              const SingleActivator(LogicalKeyboardKey.arrowLeft): () =>
                  onStep(-1),
              const SingleActivator(LogicalKeyboardKey.arrowDown): () =>
                  onStep(-1),
            },
            child: Focus(
              autofocus: true,
              child: Listener(
                onPointerSignal: (event) {
                  if (event is PointerScrollEvent &&
                      event.scrollDelta.dy != 0) {
                    GestureBinding.instance.pointerSignalResolver.register(
                      event,
                      (_) => onStep(event.scrollDelta.dy > 0 ? 1 : -1),
                    );
                  }
                },
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTapDown: (details) =>
                      onChanged(snap(details.localPosition, diameter)),
                  onPanStart: (details) =>
                      onChanged(snap(details.localPosition, diameter)),
                  onPanUpdate: (details) =>
                      onChanged(snap(details.localPosition, diameter)),
                  child: Stack(
                    children: [
                      Positioned.fill(
                        child: CustomPaint(
                          painter: _DialPainter(hours: hours, value: value),
                        ),
                      ),
                      for (final label in labels) _label(label, diameter),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    },
  );
  Widget _label(int label, double diameter) {
    final angle = (label % divisions) * 2 * math.pi / divisions - math.pi / 2;
    final radius = diameter * (hours && label >= 12 ? .255 : .395);
    final center = Offset(
      diameter / 2 + math.cos(angle) * radius,
      diameter / 2 + math.sin(angle) * radius,
    );
    final selected = label == value;
    return Positioned(
      left: center.dx - 20,
      top: center.dy - 20,
      width: 40,
      height: 40,
      child: Semantics(
        selected: selected,
        label: '$label${hours ? '时' : '分'}',
        child: TextButton(
          key: ValueKey('dial-number-$label'),
          onPressed: () => onChanged(label),
          style: TextButton.styleFrom(
            padding: EdgeInsets.zero,
            minimumSize: const Size(40, 40),
            foregroundColor: selected ? NotebookTheme.cover : NotebookTheme.ink,
            overlayColor: NotebookTheme.line,
          ),
          child: Text(
            label.toString().padLeft(label == 0 ? 2 : 1, '0'),
            style: TextStyle(
              fontFamily: 'Microsoft YaHei',
              fontSize: selected ? 21 : 16,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w400,
              decoration: selected ? TextDecoration.underline : null,
              decorationColor: NotebookTheme.cover,
              decorationThickness: 1.4,
            ),
          ),
        ),
      ),
    );
  }
}

class _DialPainter extends CustomPainter {
  const _DialPainter({required this.hours, required this.value});
  final bool hours;
  final int value;
  @override
  void paint(Canvas canvas, Size size) {
    final center = size.center(Offset.zero);
    final diameter = size.shortestSide;
    canvas.drawCircle(
      center,
      diameter / 2,
      Paint()..color = const Color.fromARGB(255, 242, 235, 218),
    );
    final divisions = hours ? 12 : 60;
    final tickPaint = Paint()
      ..color = NotebookTheme.muted.withValues(alpha: .42)
      ..strokeWidth = .8;
    for (var i = 0; i < divisions; i++) {
      final angle = i * math.pi * 2 / divisions - math.pi / 2;
      final direction = Offset(math.cos(angle), math.sin(angle));
      canvas.drawLine(
        center + direction * diameter * (hours || i % 5 == 0 ? .465 : .477),
        center + direction * diameter * .49,
        tickPaint,
      );
    }
    final angle = value % divisions * 2 * math.pi / divisions - math.pi / 2;
    final direction = Offset(math.cos(angle), math.sin(angle));
    final normal = Offset(-direction.dy, direction.dx);
    final radius = diameter * (hours && value >= 12 ? .255 : .395);
    // 细长的三角针尖停在数字内侧，数字本身高亮，不用圆形把数字盖住。
    final needle = Path()
      ..moveTo((center + normal * 1.6).dx, (center + normal * 1.6).dy)
      ..lineTo(
        (center + direction * (radius - 24)).dx,
        (center + direction * (radius - 24)).dy,
      )
      ..lineTo((center - normal * 1.6).dx, (center - normal * 1.6).dy)
      ..close();
    canvas.drawPath(needle, Paint()..color = NotebookTheme.cover);
    canvas.drawCircle(center, 3, Paint()..color = NotebookTheme.cover);
    canvas.drawCircle(center, 1.1, Paint()..color = NotebookTheme.paper);
  }

  @override
  bool shouldRepaint(_DialPainter oldDelegate) =>
      hours != oldDelegate.hours || value != oldDelegate.value;
}
