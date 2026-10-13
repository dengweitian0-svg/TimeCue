import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

import '../data/config_file_store.dart';

const _releaseApi =
    'https://api.github.com/repos/dengweitian0-svg/TimeCue/releases/latest';
const _maxAssetBytes = 2 * 1024 * 1024 * 1024;

enum SoftwareUpdateState {
  idle,
  checking,
  available,
  upToDate,
  unavailable,
  installComplete,
  downloading,
  verifying,
  waitingExit,
  failed,
}

/// 启动时从原生快照读取；更新只适用于安装目录内的托管 Go。
class SoftwareUpdateConfiguration {
  const SoftwareUpdateConfiguration({
    required this.backend,
    required this.dataDirectory,
    required this.goExecutable,
    required this.currentVersion,
    required this.installDirectory,
    required this.applicationDirectory,
    this.updaterExecutable,
    this.ownedGoPid,
    this.restartArguments = const [],
  });

  final String backend, dataDirectory, goExecutable, currentVersion;
  final String installDirectory, applicationDirectory;
  final String? updaterExecutable;
  final int? ownedGoPid;
  final List<String> restartArguments;

  String get helperPath => updaterExecutable?.isNotEmpty == true
      ? updaterExecutable!
      : _join(installDirectory, 'timecue-updater.ps1');

  bool get isSupported => isSupportedOn(Platform.isWindows);
  bool get canCheck => canCheckOn(Platform.isWindows);
  bool canCheckOn(bool windows) =>
      windows && _validVersion(currentVersion) != null;
  bool isSupportedOn(bool windows) =>
      canCheckOn(windows) &&
      backend == 'managed' &&
      p.basename(goExecutable).toLowerCase() == 'timecue-core.exe' &&
      _samePath(applicationDirectory, installDirectory) &&
      _samePath(_parent(goExecutable), installDirectory);

  factory SoftwareUpdateConfiguration.fromNative(
    Object? raw, {
    required String applicationDirectory,
  }) {
    if (raw is! Map ||
        raw['backend'] is! String ||
        raw['dataDir'] is! String ||
        raw['executable'] is! String ||
        raw['version'] is! String ||
        raw['installDirectory'] is! String) {
      throw const FormatException('原生更新配置不完整。');
    }
    final go = raw['executable'] as String;
    final install = raw['installDirectory'] as String;
    if ((raw['backend'] == 'managed' && !p.isAbsolute(go)) ||
        !p.isAbsolute(install)) {
      throw const FormatException('更新目录必须是绝对路径。');
    }
    final args = raw['restartArguments'];
    return SoftwareUpdateConfiguration(
      backend: raw['backend'] as String,
      dataDirectory: raw['dataDir'] as String,
      goExecutable: go,
      currentVersion: raw['version'] as String,
      installDirectory: install,
      applicationDirectory: applicationDirectory,
      updaterExecutable: raw['updaterExecutable'] is String
          ? raw['updaterExecutable'] as String
          : null,
      ownedGoPid: raw['ownedGoPid'] is int ? raw['ownedGoPid'] as int : null,
      restartArguments: args is List
          ? args.whereType<String>().where(_isRestartArgument).toList()
          : const [],
    );
  }
}

class SoftwareUpdateRelease {
  const SoftwareUpdateRelease({
    required this.version,
    required this.tag,
    required this.notes,
    required this.archiveName,
    required this.archiveUri,
    required this.checksumName,
    required this.checksumUri,
  });
  final String version, tag, notes, archiveName, checksumName;
  final Uri archiveUri, checksumUri;
}

abstract interface class SoftwareUpdateTransport {
  Future<SoftwareUpdateRelease?> latestRelease();
  Future<void> download(
    Uri uri,
    File destination, {
    required int maxBytes,
    required void Function(int received, int? total) onProgress,
  });
  void close();
}

/// 网络实现与平台安装入口分开，便于用确定性替身验证流程。
class HttpSoftwareUpdateTransport implements SoftwareUpdateTransport {
  HttpSoftwareUpdateTransport({HttpClient? client})
    : _client = client ?? HttpClient() {
    _client.connectionTimeout = const Duration(seconds: 15);
    _client.userAgent = 'TimeCue-Updater';
  }
  final HttpClient _client;

  @override
  Future<SoftwareUpdateRelease?> latestRelease() async {
    final request = await _client.getUrl(Uri.parse(_releaseApi));
    request.headers.set(
      HttpHeaders.acceptHeader,
      'application/vnd.github+json',
    );
    final response = await request.close().timeout(const Duration(seconds: 30));
    if (response.statusCode == HttpStatus.notFound) {
      await response.drain<void>();
      return null;
    }
    if (response.statusCode != HttpStatus.ok) {
      await response.drain<void>();
      throw HttpException('GitHub Release 查询失败（HTTP ${response.statusCode}）。');
    }
    final bytes = await _readBounded(response, 1024 * 1024);
    final decoded = jsonDecode(utf8.decode(bytes));
    return parseLatestRelease(decoded);
  }

  @override
  Future<void> download(
    Uri uri,
    File destination, {
    required int maxBytes,
    required void Function(int received, int? total) onProgress,
  }) async {
    final request = await _client
        .getUrl(uri)
        .timeout(const Duration(seconds: 20));
    request.headers.set(HttpHeaders.acceptHeader, 'application/octet-stream');
    final response = await request.close().timeout(const Duration(seconds: 30));
    if (response.statusCode != HttpStatus.ok) {
      await response.drain<void>();
      throw HttpException('更新文件下载失败（HTTP ${response.statusCode}）。');
    }
    final total = response.contentLength < 0 ? null : response.contentLength;
    if (total != null && total > maxBytes) {
      await response.drain<void>();
      throw const FormatException('更新文件超过大小限制。');
    }
    final output = await destination.open(mode: FileMode.write);
    var received = 0;
    try {
      await for (final chunk in response.timeout(const Duration(seconds: 45))) {
        received += chunk.length;
        if (received > maxBytes) {
          throw const FormatException('更新文件超过大小限制。');
        }
        await output.writeFrom(chunk);
        onProgress(received, total);
      }
      await output.flush();
      if (total != null && received != total) {
        throw const FormatException('更新文件下载不完整。');
      }
    } finally {
      await output.close();
    }
  }

  @override
  void close() => _client.close(force: true);
}

abstract interface class SoftwareUpdatePlatform {
  Future<bool> prepareUpdate({
    required String helperPath,
    required String planPath,
  });
  Future<void> launchUpdate({
    required String helperPath,
    required String planPath,
  });
  Future<String?> readUpdateStatus({required String statusPath});
  Future<void> commitUpdate({required String planPath});
  Future<void> cancelUpdate({required String planPath});
}

class WindowsSoftwareUpdatePlatform implements SoftwareUpdatePlatform {
  static const channel = MethodChannel('timecue/update');
  @override
  Future<bool> prepareUpdate({
    required String helperPath,
    required String planPath,
  }) async =>
      await channel.invokeMethod<bool>('prepareUpdate', {
        'helperPath': helperPath,
        'planPath': planPath,
      }) ==
      true;

  @override
  Future<void> launchUpdate({
    required String helperPath,
    required String planPath,
  }) => channel.invokeMethod<void>('launchUpdate', {
    'helperPath': helperPath,
    'planPath': planPath,
  });

  @override
  Future<String?> readUpdateStatus({required String statusPath}) async =>
      await channel.invokeMethod<String>('readUpdateStatus', {
        'statusPath': statusPath,
      });

  @override
  Future<void> cancelUpdate({required String planPath}) =>
      channel.invokeMethod<void>('cancelUpdate', {'planPath': planPath});

  @override
  Future<void> commitUpdate({required String planPath}) =>
      channel.invokeMethod<void>('commitUpdate', {'planPath': planPath});
}

/// 单个控制器拥有检查、下载、准备和启动安装的完整状态转换。
class SoftwareUpdateController extends ChangeNotifier {
  SoftwareUpdateController({
    required this.configuration,
    required this.platform,
    SoftwareUpdateTransport? transport,
    bool? windows,
    Directory? temporaryDirectory,
  }) : transport = transport ?? HttpSoftwareUpdateTransport(),
       _windows = windows ?? Platform.isWindows,
       _temporaryDirectory = temporaryDirectory;

  final SoftwareUpdateConfiguration configuration;
  final SoftwareUpdatePlatform platform;
  final SoftwareUpdateTransport transport;
  final bool _windows;
  final Directory? _temporaryDirectory;

  SoftwareUpdateState _state = SoftwareUpdateState.idle;
  SoftwareUpdateState get state => _state;
  String? _error;
  String? get error => _error;
  String? _latestVersion;
  String? get latestVersion => _latestVersion;
  String? get releaseNotes => _release?.notes;
  double? _progress;
  double? get progress => _progress;
  bool get isSupported => configuration.isSupportedOn(_windows);
  bool get canInstall => isSupported;
  bool get canCheck => configuration.canCheckOn(_windows);
  String? get supportReason => isSupported ? null : _supportMessage;
  bool _busy = false;
  bool _disposed = false;
  int _generation = 0;
  SoftwareUpdateRelease? _release;

  void _publish(SoftwareUpdateState state, {String? error}) {
    if (_disposed) return;
    _state = state;
    _error = error == null ? null : _bounded(error);
    notifyListeners();
  }

  Future<void> checkForUpdates() async {
    if (_busy) return;
    if (!canCheck) {
      _publish(SoftwareUpdateState.unavailable, error: '当前平台或版本不支持检查更新。');
      return;
    }
    ++_generation;
    _busy = true;
    _release = null;
    _latestVersion = null;
    _publish(SoftwareUpdateState.checking);
    try {
      final release = await transport.latestRelease();
      if (release == null) {
        _release = null;
        _publish(
          SoftwareUpdateState.unavailable,
          error: '暂无可用的正式 Windows x64 发行包。',
        );
      } else {
        final current = _validVersion(configuration.currentVersion)!;
        final target = _validVersion(release.version)!;
        _release = release;
        _latestVersion = release.version;
        _publish(
          _compareVersion(target, current) > 0
              ? SoftwareUpdateState.available
              : SoftwareUpdateState.upToDate,
          error: _compareVersion(target, current) > 0 ? null : '当前版本已是最新版本。',
        );
      }
    } catch (error) {
      _publish(SoftwareUpdateState.failed, error: '检查更新失败：$error');
    } finally {
      _busy = false;
    }
  }

  Future<void> restoreStatus() async {
    if (_disposed || _state != SoftwareUpdateState.idle) return;
    final generation = _generation;
    try {
      final status = await platform.readUpdateStatus(
        statusPath: _join(
          configuration.dataDirectory,
          'software-update-status.json',
        ),
      );
      if (_disposed ||
          generation != _generation ||
          _state != SoftwareUpdateState.idle) {
        return;
      }
      if (status == null || status.isEmpty) return;
      // 安装器会在更新进程重启后保留结果，页面可显示上次安装状态。
      final decoded = jsonDecode(status);
      final completedState =
          decoded is Map &&
          (decoded['state'] == 'installed' || decoded['state'] == 'completed');
      final statusVersion = decoded is Map
          ? decoded['targetVersion'] ?? decoded['version']
          : null;
      if (completedState && statusVersion == configuration.currentVersion) {
        _publish(
          SoftwareUpdateState.installComplete,
          error: decoded['message'] is String
              ? decoded['message'] as String
              : '更新已安装。',
        );
      } else if (decoded is Map && decoded['state'] == 'failed') {
        _publish(
          SoftwareUpdateState.failed,
          error: decoded['message'] is String
              ? decoded['message'] as String
              : '上次更新未完成，请查看安装器诊断。',
        );
      } else {
        _publish(
          SoftwareUpdateState.unavailable,
          error: decoded is Map && decoded['message'] is String
              ? decoded['message'] as String
              : _bounded(status),
        );
      }
    } catch (error) {
      if (_disposed ||
          generation != _generation ||
          _state != SoftwareUpdateState.idle) {
        return;
      }
      _publish(SoftwareUpdateState.failed, error: '读取更新状态失败：$error');
    }
  }

  Future<void> downloadAndInstall({
    required Future<void> Function() requestExit,
  }) async {
    if (_busy ||
        !canInstall ||
        _state != SoftwareUpdateState.available ||
        _release == null) {
      return;
    }
    ++_generation;
    _busy = true;
    Directory? work;
    var prepareStarted = false;
    String? launchedPlanPath;
    try {
      final helper = File(configuration.helperPath);
      if (!await helper.exists()) {
        throw const FormatException(
          '当前安装目录缺少 timecue-updater.ps1；请使用包含更新器的发行包。',
        );
      }
      final release = _release!;
      work = await (_temporaryDirectory ?? Directory.systemTemp).createTemp(
        'timecue-update-',
      );
      final archive = File(_join(work.path, release.archiveName));
      final checksum = File(_join(work.path, release.checksumName));
      _progress = 0;
      _publish(SoftwareUpdateState.downloading);
      await transport.download(
        release.archiveUri,
        archive,
        maxBytes: _maxAssetBytes,
        onProgress: (received, total) {
          _progress = total == null || total <= 0
              ? null
              : (received / total).clamp(0.0, 1.0);
          if (!_disposed) notifyListeners();
        },
      );
      await transport.download(
        release.checksumUri,
        checksum,
        maxBytes: 4096,
        onProgress: (_, _) {},
      );
      if (_disposed) throw StateError('软件更新界面已关闭。');
      _publish(SoftwareUpdateState.verifying);
      final staged = _join(work.path, 'staging');
      final planPath = _join(work.path, 'plan.json');
      final config = await ConfigFileStore(configuration.dataDirectory).read();
      final databaseOverride = config.databasePath;
      if (databaseOverride != null &&
          databaseOverride.startsWith('file:') &&
          databaseOverride != ':memory:') {
        throw const FormatException('当前 SQLite file: 数据源不能安全确定保护路径，暂不支持在线安装。');
      }
      final String? databasePath = databaseOverride == ':memory:'
          ? null
          : databaseOverride == null
          ? _join(_join(configuration.dataDirectory, 'db'), 'timecue.db')
          : _absolutePath(databaseOverride, configuration.dataDirectory);
      final logPath = config.logPath == null
          ? _join(configuration.dataDirectory, 'log')
          : _absolutePath(config.logPath!, configuration.dataDirectory);
      final protectedPaths = <String>{
        _join(configuration.dataDirectory, 'config.yaml'),
        _join(configuration.dataDirectory, 'software-update-status.json'),
        if (databasePath case final String path) path,
        if (databasePath case final String path) '$path-wal',
        if (databasePath case final String path) '$path-shm',
        logPath,
      }.toList();
      final plan = <String, Object?>{
        'schemaVersion': 1,
        'archivePath': archive.path,
        'checksumPath': checksum.path,
        'stagingDirectory': staged,
        'installDirectory': configuration.installDirectory,
        'currentVersion': configuration.currentVersion,
        'targetVersion': release.version,
        'dataDirectory': configuration.dataDirectory,
        'parentPid': pid,
        'statusPath': _join(
          configuration.dataDirectory,
          'software-update-status.json',
        ),
        'protectedPaths': protectedPaths,
        if (configuration.ownedGoPid case final int ownedPid)
          'ownedGoPid': ownedPid,
        'restartArguments': _safeRestartArguments(configuration),
      };
      await File(planPath).writeAsString(jsonEncode(plan), flush: true);
      final helperCopy = File(_join(work.path, 'timecue-updater.ps1'));
      await helper.copy(helperCopy.path);
      prepareStarted = true;
      final prepared = await platform.prepareUpdate(
        helperPath: helperCopy.path,
        planPath: planPath,
      );
      if (!prepared) {
        throw const FormatException('更新包校验或准备失败，请查看更新状态诊断。');
      }
      if (_disposed) throw StateError('软件更新界面已关闭。');
      _publish(SoftwareUpdateState.waitingExit);
      // launch 超时也可能已经启动 helper；先保留计划路径以便明确取消。
      launchedPlanPath = planPath;
      await platform.launchUpdate(
        helperPath: helperCopy.path,
        planPath: planPath,
      );
      if (_disposed) throw StateError('软件更新界面已关闭。');
      await platform.commitUpdate(planPath: planPath);
      if (_disposed) throw StateError('软件更新界面已关闭。');
      // ready 握手及 commit marker 写入成功后，才开始应用退出流程。
      await requestExit();
    } catch (error) {
      if (launchedPlanPath != null) {
        try {
          await platform.cancelUpdate(planPath: launchedPlanPath);
        } catch (_) {}
      }
      _publish(SoftwareUpdateState.failed, error: '更新失败：$error');
    } finally {
      _progress = null;
      _busy = false;
      // helper 启动后保留工作目录供取消、回滚和崩溃恢复读取。
      if (!prepareStarted && launchedPlanPath == null && work != null) {
        try {
          await work.delete(recursive: true);
        } catch (_) {}
      }
    }
  }

  String get _supportMessage => !_windows
      ? '软件更新仅支持 Windows。'
      : configuration.backend != 'managed'
      ? '软件更新仅支持托管运行模式。'
      : p.basename(configuration.goExecutable).toLowerCase() !=
                'timecue-core.exe' ||
            !_samePath(
              _parent(configuration.goExecutable),
              configuration.installDirectory,
            )
      ? '自定义 Go 可执行文件不在应用安装目录，无法安全更新。'
      : '当前版本号无效，无法安装更新。';

  @override
  void dispose() {
    _disposed = true;
    transport.close();
    super.dispose();
  }
}

SoftwareUpdateRelease? parseLatestRelease(Object? value) {
  if (value is! Map) {
    throw const FormatException('GitHub 返回的发行版本无效。');
  }
  if (value['draft'] == true || value['prerelease'] == true) return null;
  if (value['draft'] != false || value['prerelease'] != false) {
    throw const FormatException('GitHub 返回的发行状态无效。');
  }
  final tag = value['tag_name'];
  final version = _tagVersion(tag);
  if (version == null) throw const FormatException('发行标签必须是 v主.次.补丁+数字构建号。');
  final assets = value['assets'];
  if (assets is! List) throw const FormatException('发行版本缺少文件列表。');
  final archives = <Map>[];
  for (final item in assets.whereType<Map>()) {
    final name = item['name'];
    if (name is String && _archiveVersion(name) == version) archives.add(item);
  }
  if (archives.isEmpty) return null;
  if (archives.length != 1) {
    throw const FormatException('发行版本必须包含唯一的完整 Windows x64 ZIP。');
  }
  final archive = archives.single;
  final archiveName = archive['name'] as String;
  final checksumName = '$archiveName.sha256';
  final checksumAssets = assets
      .whereType<Map>()
      .where((asset) => asset['name'] == checksumName)
      .toList();
  final archiveUri = Uri.tryParse(
    archive['browser_download_url']?.toString() ?? '',
  );
  final checksumUri = checksumAssets.length == 1
      ? Uri.tryParse(
          checksumAssets.single['browser_download_url']?.toString() ?? '',
        )
      : null;
  if (checksumAssets.length != 1 ||
      !_validAssetUri(archiveUri) ||
      !_validAssetUri(checksumUri)) {
    throw const FormatException('发行版本必须有与 ZIP 精确匹配的 SHA-256 文件。');
  }
  return SoftwareUpdateRelease(
    version: version,
    tag: tag as String,
    notes: value['body'] is String ? _bounded(value['body'] as String) : '',
    archiveName: archiveName,
    archiveUri: archiveUri!,
    checksumName: checksumName,
    checksumUri: checksumUri!,
  );
}

String? _tagVersion(Object? tag) {
  if (tag is! String || !tag.startsWith('v')) return null;
  final version = tag.substring(1);
  return _validVersion(version) == null ? null : version;
}

String? _archiveVersion(String name) {
  final match = RegExp(
    r'^TimeCue-(\d+\.\d+\.\d+\+\d+)-windows-x64(?:-\d{8}-\d{6}-\d+)?\.zip$',
  ).firstMatch(name);
  return match?.group(1);
}

List<int>? _validVersion(String value) {
  final match = RegExp(
    r'^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\+(0|[1-9]\d*)$',
  ).firstMatch(value);
  if (match == null) return null;
  final parts = <int>[];
  for (var i = 1; i <= 4; i++) {
    final part = int.tryParse(match.group(i)!);
    if (part == null) return null;
    parts.add(part);
  }
  return parts;
}

int _compareVersion(List<int> left, List<int> right) {
  for (var index = 0; index < left.length; index++) {
    final result = left[index].compareTo(right[index]);
    if (result != 0) return result;
  }
  return 0;
}

bool _validAssetUri(Uri? uri) =>
    uri != null && uri.scheme == 'https' && uri.host == 'github.com';

bool _isRestartArgument(String argument) =>
    argument.startsWith('--data-dir=') ||
    argument == '--backend=managed' ||
    argument.startsWith('--go-executable=');

List<String> _safeRestartArguments(SoftwareUpdateConfiguration config) => [
  '--backend=managed',
  '--data-dir=${config.dataDirectory}',
  '--go-executable=${config.goExecutable}',
];

Future<List<int>> _readBounded(HttpClientResponse response, int limit) async {
  final result = <int>[];
  await for (final chunk in response.timeout(const Duration(seconds: 30))) {
    if (result.length + chunk.length > limit) {
      throw const FormatException('GitHub 响应超过大小限制。');
    }
    result.addAll(chunk);
  }
  return result;
}

String _bounded(String value) =>
    value.length <= 1000 ? value : value.substring(0, 1000);
String _join(String directory, String name) =>
    '$directory${Platform.pathSeparator}$name';
String _parent(String path) => p.dirname(path);
String _absolutePath(String path, String base) =>
    p.normalize(p.isAbsolute(path) ? path : p.join(base, path));
bool _samePath(String left, String right) =>
    _normalizePath(left) == _normalizePath(right);
String _normalizePath(String path) =>
    path.replaceAll('/', '\\').replaceAll(RegExp(r'\\+$'), '').toLowerCase();
