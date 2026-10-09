import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../../theme/notebook_theme.dart';
import 'notebook_view_model.dart';

class TodaySpread extends StatelessWidget {
  const TodaySpread({super.key, required this.model});
  final NotebookViewModel model;
  @override
  Widget build(BuildContext context) => Row(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Expanded(
        child: PaperPage(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              PageHeading(
                '今日安排',
                '${model.repository.demoNow.month} 月 ${model.repository.demoNow.day} 日 · ${model.repository.today.where((e) => e.acknowledgedAt != null).length} / ${model.repository.today.length} 已完成',
              ),
              for (final entry in model.repository.today)
                TriggerRow(
                  entry: entry,
                  onTap: () => showTriggerDetails(context, entry),
                ),
              if (model.repository.hasMoreOn(model.repository.demoNow))
                TextButton(
                  onPressed: model.repository.loading
                      ? null
                      : () => model.repository.loadMoreOn(
                          model.repository.demoNow,
                        ),
                  child: const Text('加载更多安排'),
                ),
              const SizedBox(height: 28),
              const Text(
                '一步一步，慢慢来。\n每一个小小的完成，都值得被记下。',
                style: TextStyle(
                  fontFamily: 'SimSun',
                  fontSize: 12,
                  height: 1.8,
                  color: NotebookTheme.muted,
                ),
              ),
            ],
          ),
        ),
      ),
      Expanded(
        child: PaperPage(
          right: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              PageHeading(
                '待确认',
                '${model.repository.pendingTotal} 件小事，等你轻轻回应',
                eyebrow: 'WAITING FOR YOUR REPLY',
              ),
              if (model.repository.loading) const LinearProgressIndicator(),
              if (model.repository.refreshError != null)
                TextButton.icon(
                  onPressed: model.refresh,
                  icon: const Icon(Icons.refresh),
                  label: Text('读取失败，重试 · ${model.repository.refreshError}'),
                ),
              if (model.repository.pendingTotal > 0) ...[
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text(
                      '已选 ${model.selected.length} 条',
                      style: const TextStyle(fontSize: 12),
                    ),
                    TextButton(
                      onPressed: model.busy || model.repository.loading
                          ? null
                          : model.selectPage,
                      child: const Text('全选本页'),
                    ),
                    TextButton(
                      onPressed: model.busy ? null : model.clearSelection,
                      child: const Text('清空'),
                    ),
                    FilledButton(
                      onPressed:
                          model.busy ||
                              model.repository.loading ||
                              model.selected.isEmpty
                          ? null
                          : () => model.confirm(model.selected),
                      child: const Text('确认选中'),
                    ),
                  ],
                ),
                for (final entry in model.pendingPage)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Checkbox(
                              value: model.selected.contains(entry.id),
                              onChanged: model.busy || model.repository.loading
                                  ? null
                                  : (_) => model.toggle(entry.id),
                              semanticLabel:
                                  '选择 ${entry.name} ${timeLabel(entry.plannedAt)}',
                            ),
                            Expanded(child: Text(entry.name)),
                          ],
                        ),
                        Text(
                          '${entry.plannedAt.month} 月 ${entry.plannedAt.day} 日 · ${timeLabel(entry.plannedAt)} · ${entry.delivery == 'failed'
                              ? '投递失败'
                              : entry.delivery == 'sent'
                              ? '已提醒'
                              : '待投递'}',
                          style: const TextStyle(
                            fontSize: 11,
                            color: NotebookTheme.muted,
                          ),
                        ),
                        const SizedBox(height: 10),
                        if (entry.description.isNotEmpty)
                          Text(
                            entry.description,
                            style: const TextStyle(
                              fontSize: 12,
                              color: NotebookTheme.muted,
                            ),
                          ),
                        OutlinedButton.icon(
                          onPressed: model.busy || model.repository.loading
                              ? null
                              : () => model.confirm({entry.id}),
                          icon: const Icon(Icons.check, size: 16),
                          label: const Text('确认完成'),
                        ),
                        const Divider(),
                      ],
                    ),
                  ),
                Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    IconButton(
                      onPressed:
                          model.busy ||
                              model.repository.loading ||
                              model.page == 0
                          ? null
                          : () => model.turnPage(-1),
                      tooltip: '上一页',
                      icon: const Icon(Icons.chevron_left),
                    ),
                    Text('${model.page + 1} / ${model.pageCount}'),
                    IconButton(
                      onPressed:
                          model.busy ||
                              model.repository.loading ||
                              model.page + 1 >= model.pageCount
                          ? null
                          : () => model.turnPage(1),
                      tooltip: '下一页',
                      icon: const Icon(Icons.chevron_right),
                    ),
                  ],
                ),
              ] else if (model.repository.hasSnapshot)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 42),
                  child: Text(
                    '✓  都回应好了，休息一下吧。',
                    style: TextStyle(color: NotebookTheme.sage),
                  ),
                ),
              if (model.error != null)
                Text(
                  model.error!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
            ],
          ),
        ),
      ),
    ],
  );
}

Future<void> showTriggerDetails(
  BuildContext context,
  TriggerSummary entry,
) => showDialog<void>(
  context: context,
  builder: (context) => AlertDialog(
    title: Text(entry.name),
    content: SelectableText(
      '${entry.plannedAt.month} 月 ${entry.plannedAt.day} 日 ${timeLabel(entry.plannedAt)}\n${entry.description}\n${entry.acknowledgedAt != null
          ? '已完成'
          : entry.source == 'projected' || entry.delivery.isEmpty
          ? '未来安排'
          : entry.delivery == 'failed'
          ? '投递失败 · 等待你的确认'
          : entry.delivery == 'pending' || entry.delivery == 'loaded'
          ? '待投递 · 等待你的确认'
          : '已提醒 · 等待你的确认'}',
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  ),
);

class TriggerRow extends StatelessWidget {
  const TriggerRow({super.key, required this.entry, this.onTap});
  final TriggerSummary entry;
  final VoidCallback? onTap;
  @override
  Widget build(BuildContext context) => Semantics(
    label: entry.acknowledgedAt == null ? null : '${entry.name}，已完成',
    child: InkWell(
      onTap: onTap,
      child: Container(
        constraints: const BoxConstraints(minHeight: 66),
        decoration: const BoxDecoration(
          border: Border(bottom: BorderSide(color: NotebookTheme.line)),
        ),
        padding: const EdgeInsets.symmetric(vertical: 16),
        child: Row(
          children: [
            SizedBox(
              width: 62,
              child: Text(
                timeLabel(entry.plannedAt),
                style: const TextStyle(
                  color: NotebookTheme.muted,
                  fontFamily: 'Georgia',
                ),
              ),
            ),
            Expanded(
              child: Text(
                entry.name,
                style: TextStyle(
                  color: entry.acknowledgedAt == null
                      ? NotebookTheme.ink
                      : NotebookTheme.sage,
                ),
              ),
            ),
            if (entry.acknowledgedAt != null)
              const Icon(
                Icons.check_circle_outline,
                color: NotebookTheme.sage,
                size: 20,
              ),
            if (onTap != null)
              const Icon(
                Icons.chevron_right,
                size: 18,
                color: NotebookTheme.muted,
              ),
          ],
        ),
      ),
    ),
  );
}

class CalendarSpread extends StatelessWidget {
  const CalendarSpread({
    super.key,
    required this.model,
    required this.onNavigate,
    required this.onManage,
    required this.editor,
  });
  final NotebookViewModel model;
  final Future<void> Function(VoidCallback) onNavigate;
  final VoidCallback onManage;
  final Widget? editor;
  String get daySummary {
    final count = model.repository.openScheduleCountOn(
      model.selectedDate ?? model.repository.demoNow,
    );
    if (model.right is LoadingDatePage) return '正在读取日程…';
    if (model.right is ErrorDatePage) return '日程数量读取失败';
    if (count == null) {
      return model.repository.loading ? '正在读取日程…' : '日程数量读取失败';
    }
    return count == 0 ? '无日程' : '当日共 $count 个日程';
  }

  @override
  Widget build(BuildContext context) => Row(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Expanded(
        child: PaperPage(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const PageHeading(
                '日历',
                '翻到某一天，看看那天的小事。',
                eyebrow: 'DAYS TO REMEMBER',
              ),
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  IconButton(
                    onPressed: () => onNavigate(() => model.changeMonth(-1)),
                    tooltip: '上个月',
                    icon: const Icon(Icons.chevron_left),
                  ),
                  Flexible(
                    child: Text(
                      '${model.month.year} 年 ${model.month.month} 月',
                      style: const TextStyle(
                        fontSize: 20,
                        fontFamily: 'SimSun',
                      ),
                    ),
                  ),
                  IconButton(
                    onPressed: () => onNavigate(() => model.changeMonth(1)),
                    tooltip: '下个月',
                    icon: const Icon(Icons.chevron_right),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              MonthGrid(
                model: model,
                onSelect: (date) => onNavigate(() => model.selectDate(date)),
              ),
              const SizedBox(height: 24),
              TextButton.icon(
                onPressed: onManage,
                icon: const Icon(Icons.list_alt),
                label: const Text('管理日程 · 含已暂停'),
              ),
              Text(
                daySummary,
                style: const TextStyle(
                  fontSize: 12,
                  color: NotebookTheme.muted,
                ),
              ),
            ],
          ),
        ),
      ),
      Expanded(
        child: PaperPage(
          right: true,
          child: switch (model.right) {
            BlankPage() => const SizedBox(key: ValueKey('calendar-blank')),
            LoadingSchedulePage() || LoadingDatePage() => const Center(
              child: CircularProgressIndicator(),
            ),
            ErrorDatePage(:final date) => Column(
              children: [
                Text(model.repository.refreshError ?? '读取日期失败'),
                TextButton(
                  onPressed: () => model.selectDate(date),
                  child: const Text('重试'),
                ),
              ],
            ),
            ScheduleErrorPage(:final id, :final message) => Column(
              children: [
                Text(message),
                TextButton(
                  onPressed: () => model.edit(id),
                  child: const Text('重试读取日程'),
                ),
                TextButton(onPressed: model.back, child: const Text('返回当天安排')),
              ],
            ),
            DateListPage(:final date) => Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                PageHeading('${date.month} 月 ${date.day} 日', '这些小事，在这一天相遇。'),
                if (model.repository.entriesOn(date).isEmpty)
                  const Text(
                    '这一天留白，也很好。',
                    style: TextStyle(color: NotebookTheme.muted),
                  ),
                for (final entry in model.repository.entriesOn(date))
                  TriggerRow(
                    entry: entry,
                    onTap:
                        model.repository.supportsRuleChanges &&
                            (entry.editable ||
                                !model.repository.serverPagination)
                        ? () => onNavigate(() => model.edit(entry.scheduleId))
                        : () => showTriggerDetails(context, entry),
                  ),
                if (model.repository.hasMoreOn(date))
                  TextButton(
                    onPressed: model.repository.loading
                        ? null
                        : () => model.repository.loadMoreOn(date),
                    child: const Text('加载更多安排'),
                  ),
              ],
            ),
            EditingPage() => editor ?? const SizedBox(),
          },
        ),
      ),
    ],
  );
}

class MonthGrid extends StatelessWidget {
  const MonthGrid({super.key, required this.model, required this.onSelect});
  final NotebookViewModel model;
  final ValueChanged<DateTime> onSelect;
  @override
  Widget build(BuildContext context) {
    final start = model.month.weekday - 1;
    final days = DateTime.utc(model.month.year, model.month.month + 1, 0).day;
    return Column(
      children: [
        Row(
          children: [
            for (final label in ['一', '二', '三', '四', '五', '六', '日'])
              Expanded(
                child: Center(
                  child: Text(
                    label,
                    style: const TextStyle(
                      color: NotebookTheme.muted,
                      fontSize: 12,
                    ),
                  ),
                ),
              ),
          ],
        ),
        const SizedBox(height: 8),
        GridView.builder(
          shrinkWrap: true,
          physics: const NeverScrollableScrollPhysics(),
          itemCount: ((start + days) / 7).ceil() * 7,
          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: 7,
          ),
          itemBuilder: (context, index) {
            final day = index - start + 1;
            if (day < 1 || day > days) return const SizedBox();
            final date = DateTime.utc(model.month.year, model.month.month, day);
            final selected =
                model.selectedDate != null &&
                sameDay(date, model.selectedDate!);
            final today = sameDay(date, model.repository.demoNow);
            final marked = model.repository.markedOn(date);
            return Padding(
              padding: const EdgeInsets.all(2),
              child: Semantics(
                selected: selected,
                label: '${date.month}月$day日${marked ? '，有安排' : ''}',
                child: TextButton(
                  style: TextButton.styleFrom(
                    padding: EdgeInsets.zero,
                    minimumSize: const Size(40, 40),
                    backgroundColor: selected ? NotebookTheme.cover : null,
                    foregroundColor: selected
                        ? NotebookTheme.paper
                        : NotebookTheme.ink,
                    shape: CircleBorder(
                      side: today
                          ? const BorderSide(color: NotebookTheme.cover)
                          : BorderSide.none,
                    ),
                  ),
                  onPressed: () => onSelect(date),
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      Text('$day'),
                      // 所有日期都保留相同的标记高度，避免有安排的数字被向上挤。
                      Visibility(
                        visible: marked,
                        maintainSize: true,
                        maintainAnimation: true,
                        maintainState: true,
                        child: Text(
                          '·',
                          style: TextStyle(
                            height: .7,
                            color: selected
                                ? NotebookTheme.paper
                                : NotebookTheme.muted,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            );
          },
        ),
      ],
    );
  }
}

class NotebookFrame extends StatelessWidget {
  const NotebookFrame({
    super.key,
    required this.model,
    required this.onClose,
    required this.onManage,
    required this.onTab,
    required this.spread,
  });
  final NotebookViewModel model;
  final VoidCallback onClose;
  final VoidCallback onManage;
  final ValueChanged<NotebookTab> onTab;
  final Widget spread;
  @override
  Widget build(BuildContext context) => Stack(
    clipBehavior: Clip.none,
    children: [
      Positioned.fill(
        top: 32,
        right: 16,
        child: Container(
          decoration: BoxDecoration(
            color: NotebookTheme.cover,
            borderRadius: BorderRadius.circular(8),
            boxShadow: const [
              BoxShadow(
                color: Color.fromARGB(128, 48, 32, 21),
                blurRadius: 70,
                offset: Offset(0, 25),
              ),
            ],
          ),
          padding: const EdgeInsets.fromLTRB(8, 8, 8, 34),
          child: LayoutBuilder(
            builder: (context, constraints) {
              // 小窗口和高文本缩放保留两张纸页，允许横向访问，不压缩字号。
              final width =
                  constraints.maxWidth < 840 ||
                      MediaQuery.textScalerOf(context).scale(14) > 20
                  ? 840.0
                  : constraints.maxWidth;
              return SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: SizedBox(
                  width: width,
                  height: constraints.maxHeight,
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(3),
                    child: spread,
                  ),
                ),
              );
            },
          ),
        ),
      ),
      Positioned(
        top: 0,
        left: 28,
        child: Row(
          children: [
            for (final tab in NotebookTab.values)
              Padding(
                padding: const EdgeInsets.only(right: 8),
                child: Semantics(
                  selected: model.tab == tab,
                  child: TextButton(
                    autofocus: tab == NotebookTab.today,
                    style: TextButton.styleFrom(
                      backgroundColor: model.tab == tab
                          ? NotebookTheme.paper
                          : const Color.fromARGB(255, 219, 202, 174),
                      shape: const RoundedRectangleBorder(
                        borderRadius: BorderRadius.vertical(
                          top: Radius.circular(7),
                        ),
                      ),
                      padding: const EdgeInsets.symmetric(horizontal: 24),
                      minimumSize: const Size(80, 40),
                    ),
                    onPressed: () => onTab(tab),
                    child: Text(tab == NotebookTab.today ? '今天' : '日历'),
                  ),
                ),
              ),
          ],
        ),
      ),
      Positioned(
        top: 6,
        right: 0,
        child: IconButton.filledTonal(
          onPressed: onClose,
          tooltip: '合上日程本',
          icon: const Icon(Icons.close),
        ),
      ),
      Positioned(
        bottom: 10,
        left: 28,
        right: 44,
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            TextButton.icon(
              onPressed: onManage,
              style: TextButton.styleFrom(
                foregroundColor: NotebookTheme.paper,
                textStyle: const TextStyle(fontSize: 11),
                padding: const EdgeInsets.symmetric(horizontal: 4),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              icon: const Icon(Icons.list_alt, size: 14),
              label: const Text('管理日程'),
            ),
            Text(
              model.tab == NotebookTab.today ? '01 — 02' : '03 — 04',
              style: const TextStyle(
                fontSize: 10,
                letterSpacing: 2,
                color: NotebookTheme.paper,
              ),
            ),
          ],
        ),
      ),
    ],
  );
}
