import 'dart:async';
import 'package:flutter/foundation.dart';
import '../../contracts/notebook_repository.dart';

enum NotebookTab { today, calendar }

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
  NotebookViewModel(this.repository)
    : month = DateTime.utc(repository.demoNow.year, repository.demoNow.month) {
    repository.addListener(_refresh);
  }
  final NotebookRepository repository;
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
  final Set<String> _selected = {};
  Set<String> get selected => Set.unmodifiable(_selected);
  int page = 0;
  static const pageSize = 3;
  bool busy = false;
  String? error;
  Timer? _poll;
  bool _disposed = false;
  int _dateRequest = 0;
  int get pageCount => (repository.pendingTotal / repository.pendingPageSize)
      .ceil()
      .clamp(1, 1000000);
  List<TriggerSummary> get pendingPage => repository.serverPagination
      ? repository.pending
      : repository.pending
            .skip(page * pageSize)
            .take(pageSize)
            .toList(growable: false);
  void _refresh() {
    if (repository.serverPagination) page = repository.pendingPageIndex;
    page = page.clamp(0, pageCount - 1);
    if (!repository.serverPagination) {
      _selected.retainAll(repository.pending.map((e) => e.id));
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
    if (busy) return;
    _selected.contains(id) ? _selected.remove(id) : _selected.add(id);
    notifyListeners();
  }

  void selectPage() {
    if (!busy) {
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
    if (busy || repository.loading) return;
    if (repository.serverPagination) {
      repository.loadPendingPage(page + delta);
      return;
    }
    page = (page + delta).clamp(0, pageCount - 1);
    notifyListeners();
  }

  Future<void> confirm(Set<String> ids) async {
    if (busy || ids.isEmpty || repository.loading) return;
    busy = true;
    error = null;
    notifyListeners();
    final frozen = Set<String>.of(ids);
    try {
      await repository.confirm(frozen);
      _selected.removeAll(frozen);
    } catch (e) {
      error = '$e';
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
