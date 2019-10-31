package mylog

import (
	"context"
	"os"
	"testing"
)

func TestLogger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bl, msgChan := NewBaseLogger(ctx, Debug, 10)
	mw := NewMessageWriter(os.Stderr)
	go mw.WaitAndWrite(msgChan)

	bl.Info.Log("Hello")
	bl.Log("Hello")
	bl.Fatal.Log("World")
	cancel()
	t.Fail()
}
