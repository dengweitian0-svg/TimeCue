import 'dart:async';
import 'package:flutter/foundation.dart';
import '../../contracts/notebook_repository.dart';

enum NotebookTab { today, history, calendar, aiChat }

enum NotebookOperationStatus { success, warning, error }

typedef NotebookOperationFeedback =
    void Function(NotebookOperationStatus status, String message);

sealed class CalendarRightPage {
  const CalendarRightPage();
}

class BlankPage extends CalendarRightPage {
  const BlankPage();
}

class DateListPage extends CalendarRightPage {
  const DateListPage(this.date);
  final DateTime date;
}

class EditingPage extends CalendarRightPage {
  const EditingPage(this.date, this.scheduleId);
  final DateTime date;
  final String scheduleId;
}

class LoadingDatePage extends CalendarRightPage {
  const LoadingDatePage(this.date);
  final DateTime date;
}

class ErrorDatePage extends CalendarRightPage {
  const ErrorDatePage(this.date);
  final DateTime date;
}

class LoadingSchedulePage extends CalendarRightPage {
  const LoadingSchedulePage(this.date, this.id);
  final DateTime date;
  final String id;
}

class ScheduleErrorPage extends CalendarRightPage {
  const ScheduleErrorPage(this.date, this.id, this.message);
  final DateTime date;
  final String id, message;
}

class NotebookViewModel extends ChangeNotifier {
  NotebookViewModel(
    this.repository, {
    this.onRecovery,
    this.onOperationFeedback,
  }) : month = DateTime.utc(repository.demoNow.year, repository.demoNow.month),
       _pendingDate = repository.demoNow {
    repository.addListener(_refresh);
  }
  final NotebookRepository repository;
  final VoidCallback? onRecovery;
  final NotebookOperationFeedback? onOperationFeedback;
  NotebookTab _tab = NotebookTab.today;
  NotebookTab get tab => _tab;
  DateTime month;
  CalendarRightPage _right = const BlankPage();
  CalendarRightPage get right => _right;
  DateTime? get selectedDate => switch (_right) {
    DateListPage(:final date) => date,
    EditingPage(:final date) => date,
    LoadingDatePage(:final date) => date,
    ErrorDatePage(:final date) => date,
    LoadingSchedulePage(:final date) => date,
    ScheduleErrorPage(:final date) => date,
    _ => null,
  };
  PendingScope get pendingScope =>
      _tab == NotebookTab.history ? PendingScope.history : PendingScope.today;
  final _selections = {
    for (final scope in PendingScope.values) scope: <String>{},
  };
  final _pages = {for (final scope in PendingScope.values) scope: 0};
  DateTime _pendingDate;
  Set<String> get _selected => _selections[pendingScope]!;
  Set<String> get selected => Set.unmodifiable(_selected);
  int get page => _pages[pendingScope]!;
  set page(int value) => _pages[pendingScope] = value;
  static const pageSize = 3;
  bool busy = false;
  final _errors = <PendingScope, String?>{};
  String? get error => _errors[pendingScope];
  Timer? _poll;
  bool _disposed = false;
  int _dateRequest = 0;
  int get pendingTotal => repository.pendingTotalFor(pendingScope);
  bool get pendingLoading => repository.pendingLoadingFor(pendingScope);
  String? get pendingError => repository.pendingErrorFor(pendingScope);
  int get pageCount =>
      (pendingTotal / repository.pendingPageSize).ceil().clamp(1, 1000000);
  List<TriggerSummary> get pendingPage => repository.serverPagination
      ? repository.pendingFor(pendingScope)
      : repository
            .pendingFor(pendingScope)
            .skip(page * repository.pendingPageSize)
            .take(repository.pendingPageSize)
            .toList(growable: false);
  void _refresh() {
    if (!sameDay(_pendingDate, repository.demoNow)) {
      _pendingDate = repository.demoNow;
      for (final scope in PendingScope.values) {
        _selections[scope]!.clear();
        _errors.remove(scope);
        _pages[scope] = 0;
      }
    }
    for (final scope in PendingScope.values) {
      final count =
          (repository.pendingTotalFor(scope) / repository.pendingPageSize)
              .ceil()
              .clamp(1, 1000000);
      _pages[scope] =
          (repository.serverPagination
                  ? repository.pendingPageIndexFor(scope)
                  : _pages[scope]!)
              .clamp(0, count - 1);
      if (!repository.serverPagination) {
        _selections[scope]!.retainAll(
          repository.pendingFor(scope).map((entry) => entry.id),
        );
      }
    }
    notifyListeners();
  }

  void open() {
    _tab = NotebookTab.today;
    startPolling();
    notifyListeners();
  }

  void startPolling() {
    _poll?.cancel();
    refresh();
    _poll = Timer.periodic(const Duration(seconds: 5), (_) {
      if (!busy && !repository.loading) refresh();
    });
  }

  void close() {
    _poll?.cancel();
    _poll = null;
  }

  Future<void> refresh() async {
    if (busy) return;
    await repository.refresh(month: month, date: selectedDate);
  }

  void changeTab(NotebookTab tab) {
    ++_dateRequest;
    if (_right case LoadingSchedulePage(:final date)) {
      _right = DateListPage(date);
    }
    _tab = tab;
    notifyListeners();
  }

  void changeMonth(int offset) {
    month = DateTime.utc(month.year, month.month + offset);
    refresh();
    notifyListeners();
  }

  Future<void> selectDate(DateTime date) async {
    final request = ++_dateRequest;
    _right = DateListPage(date);
    if (repository.serverPagination) _right = LoadingDatePage(date);
    notifyListeners();
    await repository.refresh(month: month, date: date);
    if (_disposed || request != _dateRequest) return;
    _right = repository.refreshError == null
        ? DateListPage(date)
        : ErrorDatePage(date);
    notifyListeners();
  }

  Future<void> edit(String id) async {
    final date = selectedDate ?? repository.demoNow;
    final request = ++_dateRequest;
    _tab = NotebookTab.calendar;
    if (repository.serverPagination) {
      _right = LoadingSchedulePage(date, id);
      notifyListeners();
      try {
        await repository.loadSchedule(id);
      } catch (e) {
        if (!_disposed && request == _dateRequest) {
          _right = ScheduleErrorPage(date, id, '$e');
          notifyListeners();
        }
        return;
      }
    }
    if (_disposed || request != _dateRequest) return;
    _right = EditingPage(date, id);
    notifyListeners();
  }

  void back() {
    ++_dateRequest;
    if (selectedDate != null) _right = DateListPage(selectedDate!);
    notifyListeners();
  }

  void toggle(String id) {
    if (busy || !repository.pendingCurrent) return;
    _selected.contains(id) ? _selected.remove(id) : _selected.add(id);
    notifyListeners();
  }

  void selectPage() {
    if (!busy && !pendingLoading && repository.pendingCurrent) {
      _selected.addAll(pendingPage.map((e) => e.id));
      notifyListeners();
    }
  }

  void clearSelection() {
    if (!busy) {
      _selected.clear();
      notifyListeners();
    }
  }

  void turnPage(int delta) {
    if (busy || pendingLoading) return;
    if (repository.serverPagination) {
      repository.loadPendingPage(page + delta, scope: pendingScope);
      return;
    }
    page = (page + delta).clamp(0, pageCount - 1);
    notifyListeners();
  }

  Future<void> confirm(Set<String> ids) async {
    if (busy || ids.isEmpty || pendingLoading || !repository.pendingCurrent) {
      return;
    }
    if (ids.length > 500) {
      const message = '一次最多确认 500 条，请减少选择后重试。';
      _errors[pendingScope] = message;
      onOperationFeedback?.call(NotebookOperationStatus.error, message);
      notifyListeners();
      return;
    }
    final scope = pendingScope;
    busy = true;
    _errors[scope] = null;
    notifyListeners();
    final frozen = Set<String>.of(ids);
    final selection = _selected;
    try {
      await repository.confirm(frozen);
      if (_disposed) return;
      selection.removeAll(frozen);
      final refreshError = repository.refreshError;
      if (refreshError != null) {
        onOperationFeedback?.call(
          NotebookOperationStatus.warning,
          '已确认 ${frozen.length} 条提醒，但列表刷新失败：$refreshError',
        );
      } else {
        onOperationFeedback?.call(
          NotebookOperationStatus.success,
          '已确认 ${frozen.length} 条提醒',
        );
      }
    } catch (e) {
      if (!_disposed) {
        _errors[scope] = '$e';
        onOperationFeedback?.call(NotebookOperationStatus.error, '确认提醒失败：$e');
      }
    }
    if (_disposed) return;
    busy = false;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    close();
    repository.removeListener(_refresh);
    super.dispose();
  }
}
