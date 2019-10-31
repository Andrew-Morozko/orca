package ioctrl

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

type CopyMonitor struct {
	ticker *time.Ticker
	ctx    context.Context
	Close  context.CancelFunc
	Done   func() <-chan struct{}

	// Config
	bufSize               int
	monitorUpdateInterval time.Duration
	// lock protects everything below
	lock             sync.Mutex
	notificationChan <-chan struct{}
	lastNotification time.Time

	err       error
	isDone    bool
	isCorrect bool
}

type Option func(*CopyMonitor) error

func BufSize(size int) Option {
	return func(cm *CopyMonitor) error {
		if size < 1 {
			return errors.New("Buffer can't be smaller than 1 byte")
		}
		cm.bufSize = size
		return nil
	}
}

func UpdateInterval(interval time.Duration) Option {
	return func(cm *CopyMonitor) error {
		cm.monitorUpdateInterval = interval
		return nil
	}
}

func NotificationChanel(ch <-chan struct{}) Option {
	return func(cm *CopyMonitor) error {
		cm.notificationChan = ch
		return nil
	}
}
func NewCopyMonitor(ctx context.Context, opts ...Option) (cm *CopyMonitor, err error) {
	t := time.Now()
	cm = &CopyMonitor{
		bufSize:               2048,
		monitorUpdateInterval: time.Second,
		lastNotification:      t,
		notificationChan:      nil,
	}
	for _, opt := range opts {
		err = opt(cm)
		if err != nil {
			return nil, err
		}
	}
	cm.ctx, cm.Close = context.WithCancel(ctx)
	cm.Done = cm.ctx.Done
	cm.ticker = time.NewTicker(cm.monitorUpdateInterval)

	return
}

func (cm *CopyMonitor) Status() (done, isCorrect bool, err error) {
	cm.lock.Lock()
	done = cm.isDone
	isCorrect = cm.isCorrect
	err = cm.err
	cm.lock.Unlock()
	return
}

var ForsedShutdown = errors.New("Forsed Shutdown")

// func (cm *CopyMonitor) Callback(t chan time.Time) {

// }

func (cm *CopyMonitor) AddCopier(dst io.Writer, src io.Reader) {
	go func() {
		buf := make([]byte, cm.bufSize)
		var err error

		ctxDone := cm.ctx.Done()
	loop:
		for {
			select {
			case <-cm.ticker.C:
				select {
				case <-cm.notificationChan:
				default:
				}
			case <-ctxDone:
				err = ForsedShutdown
				break loop
			default:
				// copy the data
				nr, er := src.Read(buf)
				if nr > 0 {
					nw, ew := dst.Write(buf[0:nr])
					if ew != nil {
						err = ew
						break loop
					}
					if nr != nw {
						err = io.ErrShortWrite
						break loop
					}
				}
				if er != nil {
					err = er
					break loop
				}
			}
		}

		var isCorrect, imFirst bool
		cm.lock.Lock()
		if !cm.isDone {
			imFirst = true
			isCorrect = err == io.EOF || err == nil
			cm.isDone = true
			cm.ticker.Stop()
			cm.isCorrect = isCorrect
			cm.err = err
		}
		cm.lock.Unlock()

		// if c, ok := src.(io.Closer); ok {
		// 	c.Close()
		// }
		// if c, ok := dst.(io.Closer); ok {
		// 	c.Close()
		// }

		if imFirst {
			if isCorrect {
				// Give the other writers a chance to write last messages and close
				// Force shutdown if it takes longer than a second
				time.Sleep(time.Second)
			}
			cm.Close()
		}
	}()
}
