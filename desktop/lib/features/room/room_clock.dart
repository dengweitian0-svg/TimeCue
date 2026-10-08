import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import 'room_backdrop.dart';

/// 环境时间独立监听真实 UTC+8，不推进示例仓储或 Go 的业务时间。
class RoomClockController extends ValueNotifier<DateTime>
    with WidgetsBindingObserver {
  RoomClockController({DateTime Function()? now})
    : _now = now ?? beijingNow,
      super((now ?? beijingNow)()) {
    WidgetsBinding.instance.addObserver(this);
    _start();
  }
  final DateTime Function() _now;
  late final scene = ValueNotifier<String>(roomPeriod(value));
  Timer? _timer;
  void _sync() {
    value = _now();
    scene.value = roomPeriod(value);
  }

  void _start() {
    _sync();
    _timer?.cancel();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _sync());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _start();
    } else {
      _timer?.cancel();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    scene.dispose();
    super.dispose();
  }
}

class RoomClock extends StatelessWidget {
  const RoomClock({super.key, required this.clock});
  final ValueListenable<DateTime> clock;
  @override
  Widget build(BuildContext context) => RepaintBoundary(
    child: ValueListenableBuilder(
      valueListenable: clock,
      builder: (context, now, _) => Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            timeLabel(now),
            style: const TextStyle(
              fontSize: 46,
              fontWeight: FontWeight.w300,
              letterSpacing: 6,
              color: Color.fromARGB(255, 255, 249, 237),
              shadows: [Shadow(color: Colors.black45, blurRadius: 12)],
            ),
          ),
          Text(
            '${now.month} 月 ${now.day} 日 · ${['星期一', '星期二', '星期三', '星期四', '星期五', '星期六', '星期日'][now.weekday - 1]}',
            style: const TextStyle(
              fontSize: 12,
              letterSpacing: 2,
              color: Color.fromARGB(255, 255, 249, 237),
            ),
          ),
        ],
      ),
    ),
  );
}
