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
    _ => null,
  };
  final Set<String> _selected = {};
  Set<String> get selected => Set.unmodifiable(_selected);
  int page = 0;
  static const pageSize = 3;
  bool busy = false;
  String? error;
  int get pageCount =>
      (repository.pending.length / pageSize).ceil().clamp(1, 1000000);
  List<TriggerSummary> get pendingPage => repository.pending
      .skip(page * pageSize)
      .take(pageSize)
      .toList(growable: false);
  void _refresh() {
    page = page.clamp(0, pageCount - 1);
    _selected.retainAll(repository.pending.map((e) => e.id));
    notifyListeners();
  }

  void open() {
    _tab = NotebookTab.today;
    notifyListeners();
  }

  void changeTab(NotebookTab tab) {
    _tab = tab;
    notifyListeners();
  }

  void changeMonth(int offset) {
    month = DateTime.utc(month.year, month.month + offset);
    notifyListeners();
  }

  void selectDate(DateTime date) {
    _right = DateListPage(date);
    notifyListeners();
  }

  void edit(String id) {
    _right = EditingPage(selectedDate!, id);
    notifyListeners();
  }

  void back() {
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
    page = (page + delta).clamp(0, pageCount - 1);
    notifyListeners();
  }

  Future<void> confirm(Set<String> ids) async {
    if (busy || ids.isEmpty) return;
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
    busy = false;
    notifyListeners();
  }

  @override
  void dispose() {
    repository.removeListener(_refresh);
    super.dispose();
  }
}
