package exitlib

import (
	"log"
	"os"
)

type exitMsg struct {
	Message string
}

func Exit(msg string) {
	panic(exitMsg{
		Message: msg,
	})
}
func HandleExit() {
	if e := recover(); e != nil {
		if exit, ok := e.(exitMsg); ok {
			log.Println(exit.Message)
			os.Exit(1)
		}
		panic(e)
	}
}
