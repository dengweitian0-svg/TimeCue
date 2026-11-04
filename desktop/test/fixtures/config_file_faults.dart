import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;

/// 只在文件系统边界注入故障，其他操作仍读写真实隔离目录。
final class ConfigFileFaults extends IOOverrides {
  ConfigFileFaults(String directory)
    : configPath = p.join(directory, 'config.yaml'),
      envPath = p.join(directory, '.env');

  final String configPath;
  final String envPath;
  bool denyReads = false;
  bool denyEnvWrite = false;
  bool denyConfigWrite = false;
  bool denyEnvRestore = false;
  bool _envPublished = false;

  /// 拦截系统文件访问，不替换正式配置存储或其串行队列。
  @override
  File createFile(String path) => _FaultFile(super.createFile(path), this);
}

class _FaultFile implements File {
  _FaultFile(this.file, this.faults);

  final File file;
  final ConfigFileFaults faults;

  @override
  String get path => file.path;

  /// 读取故障包含敏感哨兵，确保界面不会展示系统错误原文。
  @override
  Future<String> readAsString({Encoding encoding = utf8}) {
    if (faults.denyReads &&
        (path == faults.configPath || path == faults.envPath)) {
      throw FileSystemException('private-file-content', path);
    }
    return file.readAsString(encoding: encoding);
  }

  /// 按文件边界拒绝替换，保留失败前已完成的真实文件副作用。
  @override
  Future<File> rename(String newPath) async {
    if ((newPath == faults.configPath && faults.denyConfigWrite) ||
        (newPath == faults.envPath &&
            (faults.denyEnvWrite ||
                faults.denyEnvRestore && faults._envPublished))) {
      throw FileSystemException('private-file-content', newPath);
    }
    final result = await file.rename(newPath);
    if (newPath == faults.envPath) faults._envPublished = true;
    return result;
  }

  /// 普通写入使用真实文件系统。
  @override
  Future<File> writeAsString(
    String contents, {
    FileMode mode = FileMode.write,
    Encoding encoding = utf8,
    bool flush = false,
  }) => file.writeAsString(
    contents,
    mode: mode,
    encoding: encoding,
    flush: flush,
  );

  /// 文件存在性使用真实结果。
  @override
  Future<bool> exists() => file.exists();

  /// 恢复首次保存时可能需要删除新密钥文件。
  @override
  Future<FileSystemEntity> delete({bool recursive = false}) {
    if (path == faults.envPath && faults.denyEnvRestore) {
      throw FileSystemException('private-file-content', path);
    }
    return file.delete(recursive: recursive);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
