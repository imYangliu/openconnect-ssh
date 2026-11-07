package log

import (
	"io"
	"log"
)

// OCH owns diagnostics. Upstream logging includes auth responses and packet data.
func DebugEnabled() bool                  { return false }
func Print(...any)                        {}
func Println(...any)                      {}
func Printf(string, ...any)               {}
func DebugPrint(...any)                   {}
func DebugPrintln(...any)                 {}
func DebugPrintf(string, ...any)          {}
func DumpHex([]byte)                      {}
func DebugDumpHex([]byte)                 {}
func NewLogger(prefix string) *log.Logger { return log.New(io.Discard, prefix, 0) }
