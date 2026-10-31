package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

var Log *Logger

type Logger struct {
	info  *log.Logger
	warn  *log.Logger
	err   *log.Logger
	debug *log.Logger
}

func NewLogger(verbose bool) {
	flags := log.LstdFlags
	var dbg io.Writer = io.Discard
	if verbose {
		dbg = os.Stderr
	}
	Log = &Logger{
		info:  log.New(os.Stderr, "[info]  ", flags),
		warn:  log.New(os.Stderr, "[warn]  ", flags),
		err:   log.New(os.Stderr, "[error] ", flags),
		debug: log.New(dbg, "[debug] ", flags),
	}
}

func (l *Logger) Infof(f string, a ...any)  { l.info.Output(2, fmt.Sprintf(f, a...)) }
func (l *Logger) Warnf(f string, a ...any)  { l.warn.Output(2, fmt.Sprintf(f, a...)) }
func (l *Logger) Errorf(f string, a ...any) { l.err.Output(2, fmt.Sprintf(f, a...)) }
func (l *Logger) Fatalf(f string, a ...any) { l.err.Output(2, fmt.Sprintf(f, a...)); os.Exit(1) }
func (l *Logger) Debugf(f string, a ...any) { l.debug.Output(2, fmt.Sprintf(f, a...)) }

func init() {
	if Log == nil { // pre-startup logging
		NewLogger(false)
	}
}

func macString(m [6]byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}
