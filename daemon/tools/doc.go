// Package tools holds the built-in tools: bash, read, write, edit, glob, grep and
// task.update. They are built in because they are universal and hot, but they register
// through the same ToolProvider registry modules use. There is no privileged path.
package tools
