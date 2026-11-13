package main

import _ "embed"

// version 从独立版本文件在编译时嵌入，运行时无需读取外部文件。
//
//go:embed VERSION
var version string
