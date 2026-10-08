import 'dart:ui';
import 'package:flutter/material.dart';
import 'room_geometry.dart';

String roomPeriod(DateTime beijing) => beijing.hour >= 6 && beijing.hour < 12
    ? 'morning'
    : beijing.hour >= 12 && beijing.hour < 18
    ? 'afternoon'
    : 'night';

class RoomBackdrop extends StatefulWidget {
  const RoomBackdrop({super.key, required this.period});
  final String period;
  @override
  State<RoomBackdrop> createState() => _RoomBackdropState();
}

class _RoomBackdropState extends State<RoomBackdrop> {
  String? loaded;
  int generation = 0;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    load();
  }

  @override
  void didUpdateWidget(RoomBackdrop oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.period != widget.period) load();
  }

  Future<void> load() async {
    final request = ++generation;
    final period = widget.period;
    var failed = false;
    await precacheImage(
      AssetImage('assets/room/b-anime-$period.png'),
      context,
      onError: (_, _) => failed = true,
    );
    // 新图解码成功后才交叉淡入，失败保留上一张已解码图片。
    if (mounted && request == generation && !failed) {
      setState(() => loaded = period);
    }
  }

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: Stack(
      fit: StackFit.expand,
      children: [
        const ColoredBox(color: Color.fromARGB(255, 107, 83, 62)),
        AnimatedSwitcher(
          duration: MediaQuery.disableAnimationsOf(context)
              ? Duration.zero
              : const Duration(milliseconds: 550),
          child: loaded == null
              ? const SizedBox.expand()
              : Image.asset(
                  'assets/room/b-anime-$loaded.png',
                  key: ValueKey(loaded),
                  fit: BoxFit.cover,
                  width: double.infinity,
                  height: double.infinity,
                ),
        ),
        ClipPath(
          clipper: const WindowGlassClipper(),
          child: BackdropFilter(
            filter: ImageFilter.blur(sigmaX: 1.6, sigmaY: 1.6),
            child: const ColoredBox(color: Color.fromARGB(5, 255, 255, 255)),
          ),
        ),
        const DecoratedBox(
          decoration: BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              colors: [
                Color.fromARGB(96, 53, 46, 34),
                Colors.transparent,
                Colors.transparent,
                Color.fromARGB(89, 53, 46, 34),
              ],
              stops: [0, .24, .72, 1],
            ),
          ),
        ),
      ],
    ),
  );
}
