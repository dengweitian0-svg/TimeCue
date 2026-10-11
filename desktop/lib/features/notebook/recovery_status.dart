import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import 'notification_details.dart';

String recoveryLabel(RecoveryProgress value) => switch (value.state) {
  'running' => '正在整理历史提醒，已整理 ${value.materializedCount} 条',
  'failed' => '历史整理暂时失败，已保留 ${value.materializedCount} 条记录',
  _ => '历史整理完成，已整理 ${value.materializedCount} 条',
};

class RecoveryStatus extends StatelessWidget {
  const RecoveryStatus({super.key, required this.progress, this.stale = false});
  final RecoveryProgress progress;
  final bool stale;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    mainAxisSize: MainAxisSize.min,
    children: [
      Text(
        recoveryLabel(progress),
        style: Theme.of(context).textTheme.titleSmall,
      ),
      const SizedBox(height: 8),
      Text(
        progress.missedTotal == null
            ? '遗漏总数待整理完成后确定'
            : '完整遗漏 ${progress.missedTotal} 条 · 可投递 ${progress.displayableCount} 条',
      ),
      const SizedBox(height: 8),
      Text('本轮整理上界 · ${plannedTimeLabel(progress.through)} · 北京时间'),
      if (stale)
        const Padding(
          padding: EdgeInsets.only(top: 8),
          child: Text('当前保留上次读取结果，请刷新后查看。'),
        ),
    ],
  );
}
