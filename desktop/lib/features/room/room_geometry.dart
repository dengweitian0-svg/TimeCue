import 'dart:math' as math;
import 'package:flutter/material.dart';

/// 背景和所有房间锚点共用同一 cover 变换，避免窗口裁切后热区漂移。
class RoomGeometry {
  RoomGeometry(this.viewport)
    : scale = math.max(
        viewport.width / source.width,
        viewport.height / source.height,
      );
  static const source = Size(1672, 941);
  // 原图本子的四角略留点击余量，热区随图片一起缩放、裁切。
  static const notebookCorners = [
    Offset(30, 529),
    Offset(166, 510),
    Offset(292, 544),
    Offset(151, 574),
  ];
  final Size viewport;
  final double scale;
  Offset project(Offset point) => Offset(
    (viewport.width - source.width * scale) / 2 +
        point.dx * source.width * scale,
    (viewport.height - source.height * scale) / 2 +
        point.dy * source.height * scale,
  );
  Offset get notebook => project(const Offset(.10, .58));
  Path get visibleNotebookPath => Path.combine(
    PathOperation.intersect,
    Path()..addPolygon(
      notebookCorners
          .map(
            (point) => project(
              Offset(point.dx / source.width, point.dy / source.height),
            ),
          )
          .toList(),
      true,
    ),
    Path()..addRect(Offset.zero & viewport),
  );
  // 再约束矩形边界，消除路径布尔运算在窗口边缘的浮点误差。
  Rect get visibleNotebookBounds =>
      visibleNotebookPath.getBounds().intersect(Offset.zero & viewport);
  bool get notebookVisible => !visibleNotebookBounds.isEmpty;
}

class WindowGlassClipper extends CustomClipper<Path> {
  const WindowGlassClipper();
  static const panes = [
    [
      Offset(.463, .036),
      Offset(.659, .041),
      Offset(.659, .466),
      Offset(.615, .466),
      Offset(.59, .43),
      Offset(.54, .41),
      Offset(.503, .43),
      Offset(.463, .39),
    ],
    [
      Offset(.702, .053),
      Offset(.838, 0),
      Offset(.838, .396),
      Offset(.702, .42),
    ],
    [Offset(.86, 0), Offset(.952, 0), Offset(.952, .379), Offset(.86, .394)],
  ];
  @override
  Path getClip(Size size) {
    final geometry = RoomGeometry(size);
    return Path()..addPath(_panes(geometry), Offset.zero);
  }

  Path _panes(RoomGeometry geometry) {
    final path = Path();
    for (final pane in panes) {
      path.addPolygon(pane.map(geometry.project).toList(), true);
    }
    return path;
  }

  @override
  bool shouldReclip(WindowGlassClipper oldClipper) => false;
}
