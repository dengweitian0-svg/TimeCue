const _fieldLabel =
    r'(?:(?:日程|工具|会话|运行)[ \t]*)?(?:id|revision|expected[_ ]?revision|config[_ ]?revision|version|版本号?|open)';

final _metadataLine = RegExp(
  r'^[ \t]*(?:(?:[-+*]|\d+[.)])[ \t]+)?(?:\*\*|`)?'
  '$_fieldLabel'
  r'(?:\*\*|`)?[ \t]*[:：][^\r\n]*(?:\r?\n|$)',
  caseSensitive: false,
  multiLine: true,
);

final _metadataAnnotation = RegExp(
  r'[ \t]*[（(][ \t]*(?:\*\*|`)?'
  '$_fieldLabel'
  r'(?:\*\*|`)?(?:[ \t]*[:：][ \t]*|[ \t]+)'
  r'(?:\d[\w.-]*|true|false)(?:\*\*|`)?[ \t]*(?:[）)]|$)',
  caseSensitive: false,
);

/// 仅在展示时隐藏助手正文和思考中明确标注的内部字段。
String chatDisplayText(String text) =>
    text.replaceAll(_metadataLine, '').replaceAll(_metadataAnnotation, '');
