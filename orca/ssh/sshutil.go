package orcassh

import "github.com/gliderlabs/ssh"

type PTYHandler func(ssh.Window)
type PTYHandlerSetter func(PTYHandler)

type SSHSession struct {
	ssh.Session
	ptyHandlerChan chan func(ssh.Window)
	isPty          bool
}

func Wrap(sess ssh.Session) (wrappedSess *SSHSession) {
	wrappedSess = &SSHSession{}
	wrappedSess.Session = sess

	initPty, ptyChan, isPty := sess.Pty()
	wrappedSess.isPty = isPty
	if !isPty {
		return
	}

	wrappedSess.ptyHandlerChan = make(chan func(ssh.Window))
	go func() {
		defer close(wrappedSess.ptyHandlerChan)
		curWnd := initPty.Window
		var curHandler func(ssh.Window)
		for {
			select {
			case <-wrappedSess.Context().Done():
				return
			case wnd, ok := <-ptyChan:
				if !ok {
					return
				}
				curWnd = wnd
				if curHandler != nil {
					curHandler(curWnd)
				}
			case curHandler = <-wrappedSess.ptyHandlerChan:
				if curHandler != nil {
					curHandler(curWnd)
				}
			}
		}
	}()
	return
}

func (sess *SSHSession) SetPTYHandler(newHandler PTYHandler) {
	if sess.isPty {
		sess.ptyHandlerChan <- newHandler
	}
}

func (sess *SSHSession) IsPty() bool {
	return sess.isPty
}
