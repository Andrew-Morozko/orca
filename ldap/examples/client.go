// Example of a client

package main

import (
	"context"
	"fmt"

	"log"

	"github.com/Andrew-Morozko/orca/ldap/exitlib"
	"github.com/Andrew-Morozko/orca/ldap/ldaplogin"
	"google.golang.org/grpc"
)

var grpcServerAddr = fmt.Sprintf("%s:%d", "127.0.0.1", 8888)

func main() {
	defer exitlib.HandleExit()
	conn, err := grpc.Dial(
		grpcServerAddr,
		grpc.WithInsecure(),
	)
	if err != nil {
		exitlib.Exit("Can't connect to server: " + err.Error())
	}
	defer conn.Close()
	client := ldaplogin.NewLDAPLoginClient(conn)
	reply, err := client.AuthPasswd(context.TODO(), &ldaplogin.PasswdAuthRequest{
		Login:    "testlogin",
		Password: "testpasswd",
	})
	if err != nil {
		exitlib.Exit("Reply error: " + err.Error())
	}

	log.Println(reply.GetStatus().String())
}
