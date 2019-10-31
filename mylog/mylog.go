package mylog

import (
	"context"
	"fmt"
	"io"
	"time"
)

type LogLevel uint8

const (
	Debug LogLevel = iota
	Info
	Warn
	Error
	Fatal
)

var logLevel2Name = []string{
	"[DEBUG]",
	"[INFO] ",
	"[WARN] ",
	"[ERROR]",
	"[FATAL]",
}

func (ll LogLevel) String() string {
	if ll < 5 {
		return logLevel2Name[ll]
	} else {
		return "[UNKWN]"
	}
}

type Message struct {
	Level   LogLevel
	Logger  *Logger
	Time    time.Time
	Content string
	// Error error ?

}

// from stdlib log
func itoa(buf *[]byte, i int, wid int) {
	// Assemble decimal in reverse order.
	var b [20]byte
	bp := len(b) - 1
	for i >= 10 || wid > 1 {
		wid--
		q := i / 10
		b[bp] = byte('0' + i - q*10)
		bp--
		i = q
	}
	// i < 10
	b[bp] = byte('0' + i)
	*buf = append(*buf, b[bp:]...)
}

type MessageWriter struct {
	buf []byte
	out io.Writer
}

func NewMessageWriter(out io.Writer) *MessageWriter {
	return &MessageWriter{
		out: out,
	}
}

func (mw *MessageWriter) WaitAndWrite(msgChan <-chan *Message) (err error) {
	for {
		msg, more := <-msgChan
		if !more {
			return
		}
		err = mw.WriteMessage(msg)
		if err != nil {
			return
		}
	}
}

func (mw *MessageWriter) WriteMessage(msg *Message) (err error) {
	year, month, day := msg.Time.Date()
	itoa(&mw.buf, year, 4)
	mw.buf = append(mw.buf, '/')
	itoa(&mw.buf, int(month), 2)
	mw.buf = append(mw.buf, '/')
	itoa(&mw.buf, day, 2)
	mw.buf = append(mw.buf, ' ')

	hour, min, sec := msg.Time.Clock()
	itoa(&mw.buf, hour, 2)
	mw.buf = append(mw.buf, ':')
	itoa(&mw.buf, min, 2)
	mw.buf = append(mw.buf, ':')
	itoa(&mw.buf, sec, 2)

	mw.buf = append(mw.buf, ' ')
	mw.buf = append(mw.buf, msg.Level.String()...)
	mw.buf = append(mw.buf, ' ')

	for _, pref := range msg.Logger.prefixes {
		mw.buf = append(mw.buf, pref...)
		mw.buf = append(mw.buf, ": "...)
	}
	mw.buf = append(mw.buf, msg.Content...)

	if msg.Content[len(msg.Content)-1] != '\n' {
		mw.buf = append(mw.buf, '\n')
	}
	_, err = mw.out.Write(mw.buf)
	mw.buf = mw.buf[:0]
	return
}

type MsgFormatter interface {
	Log(a ...interface{})
	Logf(format string, a ...interface{})
	Err(err error, a ...interface{})
	Errf(err error, format string, a ...interface{})
}

type dummyFormatter struct {
	MsgFormatter
}

func (df *dummyFormatter) Log(a ...interface{})                            {}
func (df *dummyFormatter) Logf(format string, a ...interface{})            {}
func (df *dummyFormatter) Err(err error, a ...interface{})                 {}
func (df *dummyFormatter) Errf(err error, format string, a ...interface{}) {}

var dummyfmt = dummyFormatter{}

type defaultFormatter struct {
	// MsgFormatter
	logger *Logger
	level  LogLevel
}

func (mf *defaultFormatter) Log(a ...interface{}) {
	mf.logger.send(mf.level, fmt.Sprint(a...))
}
func (mf *defaultFormatter) Logf(format string, a ...interface{}) {
	mf.logger.send(mf.level, fmt.Sprintf(format, a...))
}
func (mf *defaultFormatter) Err(err error, a ...interface{}) {
	if err != nil {
		msg := "error"
		if len(a) != 0 {
			msg = fmt.Sprint(a...)
		}
		mf.logger.send(mf.level, fmt.Sprintf("%s: %s", msg, err))
	}
}
func (mf *defaultFormatter) Errf(err error, format string, a ...interface{}) {
	if err != nil {
		mf.logger.send(mf.level, fmt.Sprintf("%s: %s", fmt.Sprintf(format, a...), err))
	}
}

type comboFormatter struct {
	MsgFormatter
	infoFmt MsgFormatter
	errFmt  MsgFormatter
}

func (cf *comboFormatter) Log(a ...interface{}) {
	cf.infoFmt.Log(a...)
}
func (cf *comboFormatter) Logf(format string, a ...interface{}) {
	cf.infoFmt.Logf(format, a...)
}
func (cf *comboFormatter) Err(err error, a ...interface{}) {
	cf.errFmt.Err(err, a...)
}

func newMsgFormatter(forLogger *Logger, withLevel LogLevel) MsgFormatter {
	if forLogger.Level > withLevel {
		return &dummyfmt
	} else {
		return &defaultFormatter{
			logger: forLogger,
			level:  withLevel,
		}
	}
}

type Logger struct {
	MsgFormatter
	Level    LogLevel
	prefixes []string
	msgChan  chan<- *Message

	Debug MsgFormatter
	Info  MsgFormatter
	Warn  MsgFormatter
	Error MsgFormatter
	Fatal MsgFormatter
	// Error.Print(f)
}

func (logger *Logger) send(level LogLevel, content string) {
	logger.msgChan <- &Message{
		Level:   level,
		Logger:  logger,
		Time:    time.Now(),
		Content: content,
	}
}

func newLogger(level LogLevel, msgChan chan<- *Message) *Logger {
	l := &Logger{
		Level:   level,
		msgChan: msgChan,
	}

	l.Debug = newMsgFormatter(l, Debug)
	l.Info = newMsgFormatter(l, Info)
	l.Warn = newMsgFormatter(l, Warn)
	l.Error = newMsgFormatter(l, Error)
	l.Fatal = newMsgFormatter(l, Fatal)
	l.MsgFormatter = &comboFormatter{
		infoFmt: l.Info,
		errFmt:  l.Error,
	}
	return l
}

func NewBaseLogger(ctx context.Context, level LogLevel, msgBufLen int) (*Logger, <-chan *Message) {
	var msgChan chan *Message
	if msgBufLen > 0 {
		msgChan = make(chan *Message, msgBufLen)
	} else {
		msgChan = make(chan *Message)
	}
	go func() {
		<-ctx.Done()
		close(msgChan)
	}()

	return newLogger(level, msgChan), msgChan
}

func (logger *Logger) NewWithPrefix(prefix string) *Logger {
	l := newLogger(logger.Level, logger.msgChan)
	l.prefixes = append(logger.prefixes, prefix)
	return l
}
